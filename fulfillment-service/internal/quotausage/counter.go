/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

// Package quotausage counts each tenant's quota usage from the database.
//
// The counter is driven by the quota registry (proto/quota): it walks each annotated kind's
// annotations, derives the kind's table from its proto message the same way the generic DAO
// does (dao.TableNameFor), and generates one SQL statement per counted kind that returns
// (project, dimension, amount) rows for the tenant. No kind is named in the counting logic.
//
// Each statement prices every row of the kind the way the design's counting rule says: the row's
// own dimensions, what its types use (the larger of the asked and the running type), plus what the
// row asks for beyond what OSAC has already created for it. Created resources count in their own
// kind's statement, so a parent only adds the excess: greatest(asked - linked, 0). A resource
// counts until it is archived, so the statements read the main tables without filtering on the
// deletion timestamp: every state a record can hold while it exists still counts, the failed
// states included, because the record provably holds physical resources until it is gone.
//
// A kind that carries a created_for link should consider a database index for the link's
// predicate, the way migrations 155 and 156 indexed the volume name pattern and the bare-metal
// instance cluster link: the exists side of each parent's statement runs that predicate
// against the rows of the created kind.
package quotausage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/gobuffalo/flect"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	quota "github.com/osac-project/osac/proto/quota"
)

// Counter counts a tenant's usage, one SQL statement per counted kind. Create it with NewCounter.
type Counter struct {
	logger        *slog.Logger
	kinds         map[string]*quota.Kind
	statements    map[string]string
	rowStatements map[string]string
	// linkedStatements count one parent row's linked children per dimension, the exists side
	// of the row statement without the ask it is netted against. Empty for a kind that prices
	// no creates.
	linkedStatements map[string]string
	// uncreated names the kinds the installation never creates, so a parent's stored ask for
	// them counts nothing: no rows of the kind ever arrive to net it.
	uncreated map[string]bool
}

// CounterBuilder contains the data and logic needed to create a counter.
type CounterBuilder struct {
	logger         *slog.Logger
	uncreatedKinds []string
}

// NewCounter creates a builder for a usage counter.
func NewCounter() *CounterBuilder {
	return &CounterBuilder{}
}

// SetLogger sets the logger that the counter will use.
func (b *CounterBuilder) SetLogger(value *slog.Logger) *CounterBuilder {
	b.logger = value
	return b
}

// SetUncreatedQuotaKinds names the kinds the installation never creates for a request, such as
// the automatic addresses an installation with provider networking disabled never creates. The
// counter adds no ask for them to a parent's statement, agreeing with the enforcement check,
// which prices no planned creates of them (quota.WithoutCreatesOf): no rows of the kind ever
// arrive to net the ask, so counting it would hold room nothing uses.
func (b *CounterBuilder) SetUncreatedQuotaKinds(values ...string) *CounterBuilder {
	b.uncreatedKinds = values
	return b
}

// Build creates the counter. It fails loudly when an annotation is invalid or a named uncreated
// kind has none. A counted kind whose derived table does not exist fails at query time and in the
// golden statement tests instead.
func (b *CounterBuilder) Build() (result *Counter, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	kinds, err := quota.Kinds()
	if err != nil {
		return nil, fmt.Errorf("failed to load the quota registry: %w", err)
	}
	result = &Counter{
		logger: b.logger, kinds: map[string]*quota.Kind{}, uncreated: map[string]bool{},
	}
	for _, kind := range b.uncreatedKinds {
		result.uncreated[kind] = true
	}
	for i := range kinds {
		kind := &kinds[i]
		result.kinds[kind.Name] = kind
	}
	// The uncreated kinds come from one derivation in the servers, but a name that matches no
	// annotated kind is a typo, and counting the ask it meant to skip would be silent:
	for _, name := range b.uncreatedKinds {
		if _, ok := result.kinds[name]; !ok {
			return nil, fmt.Errorf(
				"kind '%s' is named as uncreated but has no quota annotations", name,
			)
		}
	}
	if err := checkKinds(result.kinds); err != nil {
		return nil, err
	}
	result.statements = map[string]string{}
	result.rowStatements = map[string]string{}
	result.linkedStatements = map[string]string{}
	for _, kind := range result.kinds {
		if result.isType(kind.Name) {
			continue
		}
		statement, err := result.kindStatement(kind, "")
		if err != nil {
			return nil, fmt.Errorf("failed to count the usage of '%s': %w", kind.Name, err)
		}
		result.statements[kind.Name] = statement
		// The same statement filtered to one row reads that row's excess, so the quota
		// check can read a single parent's reserved room:
		rowStatement, err := result.kindStatement(kind, "and r.id = $2")
		if err != nil {
			return nil, fmt.Errorf("failed to count the usage of '%s': %w", kind.Name, err)
		}
		result.rowStatements[kind.Name] = rowStatement
		// One parent row's linked children, so an update can net the ask it grows against
		// the children it already has:
		linkedStatement, err := result.kindLinkedStatement(kind, "and r.id = $2")
		if err != nil {
			return nil, fmt.Errorf("failed to count the children of '%s': %w", kind.Name, err)
		}
		if linkedStatement != "" {
			result.linkedStatements[kind.Name] = linkedStatement
		}
	}
	return result, nil
}

// checkKinds refuses shapes the counter can't price: a type kind with more than a message
// dimension and field dimensions, and a creates field whose created kind has no link back to its
// parent.
func checkKinds(kinds map[string]*quota.Kind) error {
	types := map[string]bool{}
	for _, kind := range kinds {
		for _, field := range kind.Fields {
			if field.Through != "" {
				types[field.Through] = true
			}
		}
	}
	for _, kind := range kinds {
		if !types[kind.Name] {
			continue
		}
		for _, field := range kind.Fields {
			if field.Dimension == "" {
				return fmt.Errorf(
					"type kind '%s' has field '%s', which the usage counter can't price through a type",
					kind.Name, field.Path,
				)
			}
		}
	}
	for _, kind := range kinds {
		for _, field := range kind.Fields {
			if field.Creates == nil {
				continue
			}
			created := kinds[field.Creates.Kind]
			if _, _, err := created.LinkTo(kind.Name); err != nil {
				return fmt.Errorf("kind '%s': %w", kind.Name, err)
			}
		}
	}
	return nil
}

// missingTypeDimension prefixes the sentinel row a statement emits when a counted row references
// a type that resolves to no row. The prefix can't collide with a dimension: dimensions come from
// the annotations, which never contain a colon followed by this text.
const missingTypeDimension = "quota.type.missing:"

// missingTypeAsk adds a sentinel row to the asks when the reference carries an id or a name but
// the join resolved no row, so the count fails loudly instead of pricing the resource at zero.
// The gate is the reference's key, so an id-only reference fails the count too. A missing
// running type is legitimate, because the status that holds it appears only once the resource
// runs, so sentinels are added for asked types only. The from clause is the one the reference's
// aliases live in: the row for a stored reference, the item for a planned one.
func missingTypeAsk(keyExpr, alias, from string) string {
	return fmt.Sprintf(
		"select '%s' || %s as dimension, 1 as amount %s where %s is not null and %s.name is null",
		missingTypeDimension, keyExpr, from, keyExpr, alias,
	)
}

// Count returns the tenant's usage: for each project (the empty name is the default project), the
// amount used of each dimension. It runs one statement per counted kind, all in the transaction
// from the context.
func (c *Counter) Count(ctx context.Context, tenant string) (map[string]map[string]int64, error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return nil, err
	}
	result := map[string]map[string]int64{}
	for _, name := range quota.SortedKeys(c.kinds) {
		statement, ok := c.statements[name]
		if !ok {
			continue
		}
		rows, err := tx.Query(ctx, statement, tenant)
		if err != nil {
			return nil, fmt.Errorf("failed to count the usage of '%s': %w", name, err)
		}
		for rows.Next() {
			var project, dimension string
			var amount int64
			if err := rows.Scan(&project, &dimension, &amount); err != nil {
				rows.Close()
				return nil, fmt.Errorf("failed to count the usage of '%s': %w", name, err)
			}
			if missingType, ok := strings.CutPrefix(dimension, missingTypeDimension); ok {
				rows.Close()
				return nil, fmt.Errorf(
					"failed to count the usage of '%s': a stored row references type '%s', which no longer exists",
					name, missingType)
			}
			if result[project] == nil {
				result[project] = map[string]int64{}
			}
			result[project][dimension] += amount
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("failed to count the usage of '%s': %w", name, err)
		}
		rows.Close()
	}
	return result, nil
}

// isType reports whether the named kind is only a type other resources resolve through: a type
// itself counts toward no limit.
func (c *Counter) isType(name string) bool {
	for _, other := range c.kinds {
		for _, field := range other.Fields {
			if field.Through == name {
				return true
			}
		}
	}
	return false
}

// ParentExcess returns one parent row's reserved-but-uncreated room per dimension: the row's
// excess, greatest(asked - linked, 0), computed by the same generated statement that counts the
// tenant, filtered to that one row. A parent that doesn't exist has no room. The room counts
// only the children the system created and linked back to the parent, the way the tenant-wide
// statement does.
//
// The room and the tenant's usage are two statements at READ COMMITTED, so each sees its own
// snapshot. The check holds the tenant's row lock, which serializes every checked writer, so
// only a system writer that bypasses the check can commit between them; the window it leaves is
// accepted, because closing it would mean one statement for every parent kind.
func (c *Counter) ParentExcess(
	ctx context.Context, tenant, parentKind, parentID string,
) (map[string]int64, error) {
	statement, ok := c.rowStatements[parentKind]
	if !ok {
		return nil, fmt.Errorf("no counted kind '%s'", parentKind)
	}
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, statement, tenant, parentID)
	if err != nil {
		return nil, fmt.Errorf("failed to count the usage of '%s': %w", parentKind, err)
	}
	defer rows.Close()
	result := map[string]int64{}
	for rows.Next() {
		var project, dimension string
		var amount int64
		if err := rows.Scan(&project, &dimension, &amount); err != nil {
			return nil, fmt.Errorf("failed to count the usage of '%s': %w", parentKind, err)
		}
		if missingType, ok := strings.CutPrefix(dimension, missingTypeDimension); ok {
			return nil, fmt.Errorf(
				"failed to count the usage of '%s': a stored row references type '%s', which no longer exists",
				parentKind, missingType)
		}
		result[dimension] += amount
	}
	return result, rows.Err()
}

// kindLinkedStatement generates the statement that counts one parent row's linked children per
// dimension: the exists side of the row statement, without the ask it is netted against. It
// returns an empty string when the kind prices no creates, so it has no linked children.
func (c *Counter) kindLinkedStatement(kind *quota.Kind, rowFilter string) (string, error) {
	g := &statementGenerator{
		counter:     c,
		kind:        kind,
		table:       dao.TableNameFor(string(kind.Message.Name())),
		existsKinds: map[string]bool{},
		rowFilter:   rowFilter,
	}
	for i := range kind.Fields {
		if kind.Fields[i].Creates == nil {
			continue
		}
		if err := g.addCreates(kind.Fields[i]); err != nil {
			return "", err
		}
	}
	if len(g.exists) == 0 {
		return "", nil
	}
	return fmt.Sprintf(`
		select e.dimension as dimension, e.amount::bigint as amount
		from (
			select r.id as id, r.tenant as tenant, r.project::text as project
			from %s r
			where r.tenant = $1
				%s
		) p
		left join lateral (
			select ex.dimension as dimension, sum(ex.amount)::bigint as amount
			from (
				%s
			) ex
			group by ex.dimension
		) e on true
		where e.dimension is not null
		`,
		g.table, g.rowFilter, union(g.exists),
	), nil
}

// ParentLinked returns the amounts one parent row's linked children hold per dimension: the
// children the system created and linked back to the parent, the exists side the row statement
// nets the parent's ask against. An update nets the ask it grows against these, so a grow while
// the parent still holds surplus children adds only what runs beyond them. A parent that doesn't
// exist, or whose kind prices no creates, has no linked children.
func (c *Counter) ParentLinked(
	ctx context.Context, tenant, parentKind, parentID string,
) (map[string]int64, error) {
	statement, ok := c.linkedStatements[parentKind]
	if !ok {
		return map[string]int64{}, nil
	}
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, statement, tenant, parentID)
	if err != nil {
		return nil, fmt.Errorf("failed to count the children of '%s': %w", parentKind, err)
	}
	defer rows.Close()
	result := map[string]int64{}
	for rows.Next() {
		var dimension string
		var amount int64
		if err := rows.Scan(&dimension, &amount); err != nil {
			return nil, fmt.Errorf("failed to count the children of '%s': %w", parentKind, err)
		}
		result[dimension] += amount
	}
	return result, rows.Err()
}

// pricePair is one dimension and its amount, as SQL text evaluated per row.
type pricePair struct {
	dimension string
	amount    string
}

// kindStatement generates the statement of one counted kind. The row filter, when not empty,
// is appended to the where clause and takes the row's identifier as $2, so the same statement
// can count the whole tenant or one row of it.
func (c *Counter) kindStatement(kind *quota.Kind, rowFilter string) (string, error) {
	g := &statementGenerator{
		counter:     c,
		kind:        kind,
		table:       dao.TableNameFor(string(kind.Message.Name())),
		asks:        []string{},
		rowJoins:    []string{},
		exists:      []string{},
		existsKinds: map[string]bool{},
		rowFilter:   rowFilter,
	}
	return g.generate()
}

// statementGenerator builds the statement of one kind. The asks are the selects whose union prices
// what each row asks for; the rowJoins are the type joins those selects read; the exists are the
// selects whose union prices what OSAC has created for each row.
type statementGenerator struct {
	counter  *Counter
	kind     *quota.Kind
	table    string
	asks     []string
	rowJoins []string
	exists   []string
	// existsKinds remembers the created kinds the exists side already scans.
	existsKinds map[string]bool
	// rowFilter narrows the statement to one row, identified by $2. Empty counts the tenant.
	rowFilter string
	item      string
	aliases   int
}

func (g *statementGenerator) nextAlias(prefix string) string {
	g.aliases++
	return fmt.Sprintf("%s%d", prefix, g.aliases)
}

func (g *statementGenerator) generate() (string, error) {
	row := "r"
	if g.kind.Dimension != "" {
		g.asks = append(g.asks, selectPrice(rowDimension(g.kind.Dimension, row), "1"))
	}
	for _, field := range g.kind.Fields {
		var err error
		switch {
		case field.Dimension != "":
			g.asks = append(g.asks, selectPrice(
				rowDimension(field.Dimension, row), intCast(rowPath(row, field.Path)),
			))
		case field.Through != "":
			err = g.addThrough(field)
		case field.Creates != nil:
			err = g.addCreates(field)
		}
		if err != nil {
			return "", err
		}
	}
	if len(g.asks) == 0 {
		return "", fmt.Errorf("kind '%s' counts no dimension", g.kind.Name)
	}
	if len(g.exists) == 0 {
		return fmt.Sprintf(`
			select
				r.project::text as project,
				u.dimension as dimension,
				sum(u.amount)::bigint as amount
			from %s r
			%s
			cross join lateral (
				%s
			) u
			where r.tenant = $1
				and u.dimension is not null
				and u.dimension <> ''
				%s
			group by r.project, u.dimension
			`,
			g.table, strings.Join(g.rowJoins, "\n"), union(g.asks), g.rowFilter,
		), nil
	}
	return fmt.Sprintf(`
		select
			p.project as project,
			p.dimension as dimension,
			greatest(p.amount - coalesce(e.amount, 0), 0)::bigint as amount
		from (
			select
				r.id as id,
				r.tenant as tenant,
				r.project::text as project,
				u.dimension as dimension,
				sum(u.amount)::bigint as amount
			from %s r
			%s
			cross join lateral (
				%s
			) u
			where r.tenant = $1
				and u.dimension is not null
				and u.dimension <> ''
				%s
			group by r.id, r.tenant, r.project, u.dimension
		) p
		left join lateral (
			select
				ex.dimension as dimension,
				sum(ex.amount)::bigint as amount
			from (
				%s
			) ex
			group by ex.dimension
		) e on e.dimension = p.dimension
		`,
		g.table, strings.Join(g.rowJoins, "\n"), union(g.asks), g.rowFilter,
		union(g.exists),
	), nil
}

// addThrough prices a reference that resolves through a type: one join per reference, and the
// larger amount per dimension when the annotation names a second, running reference.
func (g *statementGenerator) addThrough(field quota.Field) error {
	typeKind := g.counter.kinds[field.Through]
	key, tenant, project := referenceExprs("r", field.Path)
	asked := g.nextAlias("t")
	g.rowJoins = append(g.rowJoins, g.typeJoin(field.Through, key, tenant, project, "r", asked))
	askedPrice := rowPrice(typeKind, asked)
	// A stored row whose asked type resolves to nothing is priced at zero by the join, so a
	// sentinel row makes the count fail loudly instead of undercounting the tenant:
	g.asks = append(g.asks, missingTypeAsk(key, asked, ""))
	runningPrice := []pricePair{}
	if field.LargerOf != "" {
		runningKey, runningTenant, runningProject := referenceExprs("r", field.LargerOf)
		running := g.nextAlias("t")
		g.rowJoins = append(g.rowJoins, g.typeJoin(
			field.Through, runningKey, runningTenant, runningProject, "r", running,
		))
		// A running reference that names a type resolving to nothing means the resource
		// runs a type the counter cannot price, so the sentinel fails the count instead
		// of letting the larger-of rule quietly read zero. An absent running reference
		// stays legitimate, because the status that holds it appears only once the
		// resource runs.
		g.asks = append(g.asks, missingTypeAsk(runningKey, running, ""))
		runningPrice = rowPrice(typeKind, running)
	}
	for i, price := range askedPrice {
		amount := price.amount
		if field.LargerOf != "" {
			amount = fmt.Sprintf("greatest(%s, %s)", amount, runningPrice[i].amount)
		}
		g.asks = append(g.asks, selectPrice(price.dimension, amount))
	}
	return nil
}

// typeJoin joins the one row of the type's table that the reference selects. The reference
// matches by id or by name: references are id-or-name with id winning, the way the enforcement
// check reads them, and rows backfilled by migration 90 carry only an id. A reference that
// carries its type's tenant selects that row exactly, by tenant and project, so a same-name type
// of another scope can't replace it. A name-only reference falls back to a type of the resource's
// own tenant, or the shared tenant. The join picks at most one row, so a name that exists in both
// scopes can't double the price.
func (g *statementGenerator) typeJoin(typeKind, keyExpr, tenantExpr, projectExpr, row, alias string) string {
	table := dao.TableNameFor(string(g.counter.kinds[typeKind].Message.Name()))
	return fmt.Sprintf(`
		left join lateral (
			select t.name as name, t.data as data
			from %s t
			where (t.name = %s or t.id = %s)
				and case
					when %s is not null and %s <> '' then t.tenant = %s and t.project::text = coalesce(%s, '')
					else t.tenant = %s.tenant or t.tenant = 'shared'
				end
			order by (t.tenant = %s.tenant) desc, t.project::text
			limit 1
		) %s on true
		`,
		table, keyExpr, keyExpr, tenantExpr, tenantExpr, tenantExpr, projectExpr, row, row, alias,
	)
}

// referenceKeyExpr builds the expression that matches a reference to its type row: the
// reference's id when it carries one, else its name.
func referenceKeyExpr(id, name string) string {
	return fmt.Sprintf("coalesce(nullif(%s, ''), nullif(%s, ''))", id, name)
}

// referenceExprs reads a stored reference's key, tenant and project from a table row.
func referenceExprs(row, path string) (key, tenant, project string) {
	return referenceKeyExpr(rowPath(row, path+".id"), rowPath(row, path+".name")),
		rowPath(row, path+".tenant"), rowPath(row, path+".project")
}

// itemReferenceExprs reads a stored reference's key, tenant and project from a jsonb item.
func itemReferenceExprs(item, path string) (key, tenant, project string) {
	return referenceKeyExpr(itemPath(item, path+".id"), itemPath(item, path+".name")),
		itemPath(item, path+".tenant"), itemPath(item, path+".project")
}

// rowPrice prices one stored row of a kind at the given alias: its message dimension and its field
// dimensions. Through references need joins and are priced by the caller. It prices a type's row
// the same way: checkKinds refused a type kind with a field the counter can't price through it,
// so every field of a type carries a dimension.
func rowPrice(kind *quota.Kind, alias string) (result []pricePair) {
	if kind.Dimension != "" {
		result = append(result, pricePair{dimension: rowDimension(kind.Dimension, alias), amount: "1"})
	}
	for _, field := range kind.Fields {
		if field.Dimension != "" {
			result = append(result, pricePair{
				dimension: rowDimension(field.Dimension, alias),
				amount:    intCast(rowPath(alias, field.Path)),
			})
		}
	}
	return result
}

// addCreates prices what a creates field asks for beyond what exists: the asked rows, and the
// linked rows of the created kind that the parent's excess is measured against.
func (g *statementGenerator) addCreates(field quota.Field) error {
	creates := field.Creates
	// A kind the installation never creates contributes no ask to the parent's statement and
	// no linked rows to the exists side, the way its writer creates nothing for it and the
	// enforcement check prices no planned creates of it: provider networking disabled means
	// no automatic addresses, so a parent that asks for them holds no room.
	if g.counter.uncreated[creates.Kind] {
		return nil
	}
	created := g.counter.kinds[creates.Kind]
	createdFor, linkField, err := created.LinkTo(g.kind.Name)
	if err != nil {
		return err
	}

	descriptor, err := g.kind.Field(field.Path)
	if err != nil {
		return err
	}
	if descriptor.Kind() == protoreflect.BoolKind {
		// A bool creates a fixed number of resources when it is true. The created kind prices
		// without an item, so only paths outside its message dimension would need one.
		count := "1"
		if creates.Count > 0 {
			count = fmt.Sprintf("%d", creates.Count)
		}
		for _, price := range plannedPrice(created, creates.Set, "") {
			g.asks = append(g.asks, fmt.Sprintf(
				"select %s as dimension, %s as amount where %s = 'true'",
				price.dimension, count, rowPath("r", field.Path),
			))
		}
	} else {
		g.item = g.nextAlias("i")
		count := "1"
		if creates.Count > 0 {
			count = fmt.Sprintf("%d", creates.Count)
		} else if creates.CountField != "" {
			count = intCast(itemPath(g.item, creates.CountField))
		}
		from := g.itemsFrom(field, descriptor)
		for _, createdField := range created.Fields {
			if createdField.Through == "" {
				continue
			}
			source, ok := creates.Set[createdField.Path]
			if !ok {
				return fmt.Errorf(
					"kind '%s' creates '%s' resolving through '%s', but sets no '%s' from the item",
					g.kind.Name, creates.Kind, createdField.Path, createdField.Path,
				)
			}
			key, tenant, project := itemReferenceExprs(g.item, source)
			alias := g.nextAlias("t")
			from += g.typeJoin(createdField.Through, key, tenant, project, "r", alias)
			// A planned child whose type resolves to nothing would ask for zero of its
			// dimensions, so the sentinel makes the count fail loudly instead:
			g.asks = append(g.asks, missingTypeAsk(key, alias, from))
			for _, price := range rowPrice(g.counter.kinds[createdField.Through], alias) {
				g.asks = append(g.asks, selectFrom(
					price.dimension, fmt.Sprintf("%s * %s", count, price.amount), from,
				))
			}
		}
		for _, price := range plannedPrice(created, creates.Set, g.item) {
			g.asks = append(g.asks, selectFrom(
				price.dimension, fmt.Sprintf("%s * %s", count, price.amount), from,
			))
		}
	}

	// The exists side: the created rows linked to the parent, priced like any stored row of their
	// kind. A link is trusted only for rows the system created. Several creates fields can name
	// the same kind; the linked rows are scanned once per kind, not once per field.
	if g.existsKinds[creates.Kind] {
		return nil
	}
	g.existsKinds[creates.Kind] = true
	row := g.nextAlias("v")
	from := fmt.Sprintf("%s %s", dao.TableNameFor(string(created.Message.Name())), row)
	var prices []string
	for _, price := range rowPrice(created, row) {
		prices = append(prices, selectPrice(price.dimension, price.amount))
	}
	for _, createdField := range created.Fields {
		if createdField.Through == "" {
			continue
		}
		key, tenant, project := referenceExprs(row, createdField.Path)
		alias := g.nextAlias("t")
		from += g.typeJoin(createdField.Through, key, tenant, project, row, alias)
		for _, price := range rowPrice(g.counter.kinds[createdField.Through], alias) {
			prices = append(prices, selectPrice(price.dimension, price.amount))
		}
	}
	link, err := linkCondition(createdFor, linkField, row, "p", g.kind.Name)
	if err != nil {
		return err
	}
	g.exists = append(g.exists, fmt.Sprintf(`
		select d.dimension as dimension, d.amount as amount
		from %s
		cross join lateral (
			%s
		) d
		where %s.tenant = p.tenant
			and %s.creator = 'system'
			and %s
		`,
		from, union(prices), row, row, link,
	))
	return nil
}

// itemsFrom builds the from clause that yields one row per item of the annotated field: each entry
// of its list, each entry of the list its message holds, each value of its map, or the field's own
// value wrapped in a one-element array. The row yields the item as the jsonb value of its `val`
// column.
func (g *statementGenerator) itemsFrom(field quota.Field, descriptor protoreflect.FieldDescriptor) string {
	segments := strings.Split(field.Path, ".")
	if field.Creates.Each != "" {
		segments = append(segments, field.Creates.Each)
	}
	data := fmt.Sprintf("r.data #> '{%s}'", strings.Join(segments, ","))
	switch {
	case descriptor.IsMap():
		return fmt.Sprintf(
			"from jsonb_each(coalesce(%s, '{}'::jsonb)) as %s(k, val)", data, g.item,
		)
	case descriptor.IsList() || field.Creates.Each != "":
		return fmt.Sprintf(
			"from jsonb_array_elements(coalesce(%s, '[]'::jsonb)) as %s(val)", data, g.item,
		)
	default:
		// A singular message field is one item when it is set, and none when it is absent, so an
		// omitted field asks for nothing.
		return fmt.Sprintf(
			"from jsonb_array_elements(case when %s is null then '[]'::jsonb else jsonb_build_array(%s) end) as %s(val)",
			data, data, g.item,
		)
	}
}

// plannedPrice prices the resources a creates annotation plans, field by field of the planned
// message. Every path of the planned message resolves through the set mapping to a path of the
// item; a path the annotation doesn't set prices nothing. With no item the price can only read
// paths outside the planned message, which is the bool case.
func plannedPrice(kind *quota.Kind, set map[string]string, item string) (result []pricePair) {
	resolve := func(path string) string {
		if item == "" {
			return "null"
		}
		source, ok := set[path]
		if !ok {
			return "null"
		}
		return itemPath(item, source)
	}
	if kind.Dimension != "" {
		result = append(result, pricePair{dimension: dimensionSQL(kind.Dimension, resolve), amount: "1"})
	}
	for _, field := range kind.Fields {
		if field.Dimension != "" {
			result = append(result, pricePair{
				dimension: dimensionSQL(field.Dimension, resolve),
				amount:    intCast(resolve(field.Path)),
			})
		}
	}
	return result
}

// linkCondition builds the condition that ties a created row to the parent it was created for.
//
// The kind label's value is the parent kind's proto name in snake_case, such as "cluster" for
// Cluster: that is the value the private servers write on automatic IP addresses and the
// attachment reconcilers match, so the link must compare against the same form.
func linkCondition(createdFor *quota.CreatedFor, linkField, row, parent, parentKind string) (string, error) {
	switch {
	case createdFor.NamePrefix != "":
		var exprs []string
		rest := createdFor.NamePrefix
		for {
			start := strings.Index(rest, "{parent}")
			if start < 0 {
				break
			}
			if literal := rest[:start]; literal != "" {
				exprs = append(exprs, sqlString(literal))
			}
			exprs = append(exprs, parent+".id")
			rest = rest[start+len("{parent}"):]
		}
		if rest != "" {
			exprs = append(exprs, sqlString(rest))
		}
		if len(exprs) == 0 {
			exprs = []string{"''"}
		}
		// LIKE instead of starts_with, because the planner can serve a prefix LIKE from the
		// pattern index on the name, and the prefix holds no wildcard characters:
		return fmt.Sprintf("%s.name like %s || '%%'", row, strings.Join(exprs, " || ")), nil
	case createdFor.IDLabel != "":
		// Containment instead of two arrow comparisons, because the gin index on the labels
		// serves containment while it can't serve either comparison:
		return fmt.Sprintf(
			"%s.labels @> jsonb_build_object(%s, %s.id, %s, '%s')",
			row, sqlString(createdFor.IDLabel), parent,
			sqlString(createdFor.KindLabel), flect.Underscore(parentKind),
		), nil
	case linkField != "":
		return fmt.Sprintf(
			"%s.data #>> '{%s,id}' = %s.id",
			row, strings.ReplaceAll(linkField, ".", ","), parent,
		), nil
	default:
		return "", fmt.Errorf("unsupported created_for link")
	}
}

// rowPath reads a proto field path from a table row: the metadata lives in columns, everything
// else in the data document. The result is parenthesized, so concatenation can't capture the path
// operator.
func rowPath(row, path string) string {
	segments := strings.Split(path, ".")
	if segments[0] == "metadata" && len(segments) == 2 {
		switch segments[1] {
		case "name", "tenant", "creator":
			return fmt.Sprintf("%s.%s", row, segments[1])
		}
	}
	return fmt.Sprintf("(%s.data #>> '{%s}')", row, strings.Join(segments, ","))
}

// itemPath reads a proto field path from a jsonb item.
func itemPath(item, path string) string {
	return fmt.Sprintf("(%s.val #>> '{%s}')", item, strings.ReplaceAll(path, ".", ","))
}

// rowDimension fills the brace paths of a dimension template from a table row.
func rowDimension(template, row string) string {
	return dimensionSQL(template, func(path string) string {
		return rowPath(row, path)
	})
}

// dimensionSQL builds the SQL text expression of a dimension template: the literal parts quoted,
// the brace paths replaced by the resolver's expression.
func dimensionSQL(template string, resolve func(string) string) string {
	rest := template
	var parts []string
	for {
		start := strings.Index(rest, "{")
		if start < 0 {
			break
		}
		end := strings.Index(rest[start:], "}")
		if end < 0 {
			break
		}
		if literal := rest[:start]; literal != "" {
			parts = append(parts, sqlString(literal))
		}
		parts = append(parts, resolve(rest[start+1:start+end]))
		rest = rest[start+end+1:]
	}
	if rest != "" {
		parts = append(parts, sqlString(rest))
	}
	if len(parts) == 0 {
		return "''"
	}
	return strings.Join(parts, " || ")
}

// intCast turns the text a jsonb path yields into a bigint amount, defaulting to zero.
func intCast(expr string) string {
	return fmt.Sprintf("coalesce((%s)::bigint, 0)", expr)
}

func sqlString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func selectPrice(dimension, amount string) string {
	return fmt.Sprintf("select %s as dimension, %s as amount", dimension, amount)
}

func selectFrom(dimension, amount, from string) string {
	return fmt.Sprintf("select %s as dimension, %s as amount %s", dimension, amount, from)
}

func union(selects []string) string {
	return strings.Join(selects, "\nunion all\n")
}
