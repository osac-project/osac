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
// annotations, maps the kind's proto message to its table, and generates one SQL statement per
// counted kind that returns (project, dimension, amount) rows for the tenant. No kind is named in
// the counting logic; the tables map below is data, like the metering tables map.
//
// Each statement prices every row of the kind the way the design's counting rule says: the row's
// own dimensions, what its types use (the larger of the asked and the running type), plus what the
// row asks for beyond what OSAC has already created for it. Created resources count in their own
// kind's statement, so a parent only adds the excess: greatest(asked - linked, 0). A resource
// counts until it is archived, so the statements read the main tables without filtering on the
// deletion timestamp. A kind's counting rules (see counting_rules.go) can release earlier: the
// states they map stop counting while the record still exists, because they provably hold
// nothing physical. The release applies both to the kind's own rows and to the linked rows a
// parent nets its ask against, so a released child never eats the parent's room.
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
	quota "github.com/osac-project/osac/proto/quota"
)

// tables maps the proto message of each quota-annotated kind to the table that stores it. Extend
// together with the quota annotations when a new counted resource or type gets annotations;
// Build fails a kind that has annotations but no table, and fails a mapped kind that carries none.
var tables = map[protoreflect.FullName]string{
	"osac.private.v1.BareMetalInstance":     "bare_metal_instances",
	"osac.private.v1.BareMetalInstanceType": "bare_metal_instance_types",
	"osac.private.v1.Cluster":               "clusters",
	"osac.private.v1.ComputeInstance":       "compute_instances",
	"osac.private.v1.DiskImage":             "disk_images",
	"osac.private.v1.ExternalIP":            "external_ips",
	"osac.private.v1.InstanceType":          "instance_types",
	"osac.private.v1.NATGateway":            "nat_gateways",
	"osac.private.v1.VirtualNetwork":        "virtual_networks",
	"osac.private.v1.Volume":                "volumes",
}

// Counter counts a tenant's usage, one SQL statement per counted kind. Create it with NewCounter.
type Counter struct {
	logger          *slog.Logger
	kinds           map[string]*quota.Kind
	stopsCountingIn map[string][]string
}

// CounterBuilder contains the data and logic needed to create a counter.
type CounterBuilder struct {
	logger        *slog.Logger
	countingRules map[string][]string
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

// SetCountingRules sets the counting rules directly, for tests. Without it the counter loads the
// rules from the file named by OSAC_QUOTA_COUNTING_RULES; an unset variable or a missing file
// means no rules.
func (b *CounterBuilder) SetCountingRules(value map[string][]string) *CounterBuilder {
	b.countingRules = value
	return b
}

// Build creates the counter. It fails loudly when the registry and the tables map disagree, so a
// new annotated kind without a table is a startup failure, not a silently uncounted resource.
// The counting rules come from the builder or, when the builder sets none, from the file named by
// OSAC_QUOTA_COUNTING_RULES; rules the counter can't apply fail the build the same way.
func (b *CounterBuilder) Build() (result *Counter, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	kinds, err := quota.Kinds()
	if err != nil {
		return nil, fmt.Errorf("failed to load the quota registry: %w", err)
	}
	rules := b.countingRules
	if rules == nil {
		if rules, err = loadCountingRules(); err != nil {
			return nil, err
		}
	}
	result = &Counter{logger: b.logger, kinds: map[string]*quota.Kind{}, stopsCountingIn: rules}
	for i := range kinds {
		kind := &kinds[i]
		result.kinds[kind.Name] = kind
		if _, ok := tables[kind.Message]; !ok {
			return nil, fmt.Errorf("no table mapped for the quota-annotated kind '%s'", kind.Message)
		}
	}
	for message := range tables {
		found := false
		for _, kind := range result.kinds {
			if kind.Message == message {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("a table is mapped for quota usage but kind '%s' has no annotations", message)
		}
	}
	if err := checkKinds(result.kinds); err != nil {
		return nil, err
	}
	if err := checkCountingRules(result.kinds, rules); err != nil {
		return nil, err
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

// missingTypeAsk adds a sentinel row to the asks when the reference names a type but the join
// resolved no row, so the count fails loudly instead of pricing the resource at zero. A missing
// running type is legitimate, because the status that holds it appears only once the resource
// runs, so sentinels are added for asked types only. The from clause is the one the reference's
// aliases live in: the row for a stored reference, the item for a planned one.
func missingTypeAsk(nameExpr, alias, from string) string {
	return fmt.Sprintf(
		"select '%s' || %s as dimension, 1 as amount %s where %s is not null and %s.name is null",
		missingTypeDimension, nameExpr, from, nameExpr, alias,
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
		kind := c.kinds[name]
		if c.isType(name) {
			continue
		}
		statement, err := c.kindStatement(kind)
		if err != nil {
			return nil, fmt.Errorf("failed to count the usage of '%s': %w", kind.Name, err)
		}
		rows, err := tx.Query(ctx, statement, tenant)
		if err != nil {
			return nil, fmt.Errorf("failed to count the usage of '%s': %w", kind.Name, err)
		}
		for rows.Next() {
			var project, dimension string
			var amount int64
			if err := rows.Scan(&project, &dimension, &amount); err != nil {
				rows.Close()
				return nil, fmt.Errorf("failed to count the usage of '%s': %w", kind.Name, err)
			}
			if missingType, ok := strings.CutPrefix(dimension, missingTypeDimension); ok {
				rows.Close()
				return nil, fmt.Errorf(
					"failed to count the usage of '%s': a stored row references type '%s', which no longer exists",
					kind.Name, missingType)
			}
			if result[project] == nil {
				result[project] = map[string]int64{}
			}
			result[project][dimension] += amount
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("failed to count the usage of '%s': %w", kind.Name, err)
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

// pricePair is one dimension and its amount, as SQL text evaluated per row.
type pricePair struct {
	dimension string
	amount    string
}

// kindStatement generates the statement of one counted kind.
func (c *Counter) kindStatement(kind *quota.Kind) (string, error) {
	g := &statementGenerator{
		counter:     c,
		kind:        kind,
		table:       tables[kind.Message],
		asks:        []string{},
		rowJoins:    []string{},
		exists:      []string{},
		existsKinds: map[string]bool{},
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
	item        string
	aliases     int
}

func (g *statementGenerator) nextAlias(prefix string) string {
	g.aliases++
	return fmt.Sprintf("%s%d", prefix, g.aliases)
}

// stopsCountingIn builds the SQL filter that stops counting the rows of the named kind whose
// status.state is one of the mapped states: the release map of the quota state standard. Empty
// when no state of the kind is mapped, so the default changes nothing.
func (g *statementGenerator) stopsCountingIn(kind, row string) string {
	states := g.counter.stopsCountingIn[kind]
	if len(states) == 0 {
		return ""
	}
	quoted := make([]string, len(states))
	for i, state := range states {
		quoted[i] = sqlString(state)
	}
	return fmt.Sprintf(
		"and coalesce(%s.data #>> '{status,state}', '') not in (%s)",
		row, strings.Join(quoted, ", "),
	)
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
			g.table, strings.Join(g.rowJoins, "\n"), union(g.asks), g.stopsCountingIn(g.kind.Name, "r"),
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
		g.table, strings.Join(g.rowJoins, "\n"), union(g.asks), g.stopsCountingIn(g.kind.Name, "r"), union(g.exists),
	), nil
}

// addThrough prices a reference that resolves through a type: one join per reference, and the
// larger amount per dimension when the annotation names a second, running reference.
func (g *statementGenerator) addThrough(field quota.Field) error {
	typeKind := g.counter.kinds[field.Through]
	name, tenant, project := referenceExprs("r", field.Path)
	asked := g.nextAlias("t")
	g.rowJoins = append(g.rowJoins, g.typeJoin(field.Through, name, tenant, project, "r", asked))
	askedPrice := rowPrice(typeKind, asked)
	// A stored row whose asked type resolves to nothing is priced at zero by the join, so a
	// sentinel row makes the count fail loudly instead of undercounting the tenant:
	g.asks = append(g.asks, missingTypeAsk(name, asked, ""))
	runningPrice := []pricePair{}
	if field.LargerOf != "" {
		runningName, runningTenant, runningProject := referenceExprs("r", field.LargerOf)
		running := g.nextAlias("t")
		g.rowJoins = append(g.rowJoins, g.typeJoin(
			field.Through, runningName, runningTenant, runningProject, "r", running,
		))
		// A running reference that names a type resolving to nothing means the resource
		// runs a type the counter cannot price, so the sentinel fails the count instead
		// of letting the larger-of rule quietly read zero. An absent running reference
		// stays legitimate, because the status that holds it appears only once the
		// resource runs.
		g.asks = append(g.asks, missingTypeAsk(runningName, running, ""))
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

// typeJoin joins the one row of the type's table that the reference selects. A reference that
// carries its type's tenant selects that row exactly, by tenant and project, so a same-name type
// of another scope can't replace it. A name-only reference falls back to a type of the resource's
// own tenant, or the shared tenant. The join picks at most one row, so a name that exists in both
// scopes can't double the price.
func (g *statementGenerator) typeJoin(typeKind, nameExpr, tenantExpr, projectExpr, row, alias string) string {
	table := tables[g.counter.kinds[typeKind].Message]
	return fmt.Sprintf(`
		left join lateral (
			select t.name as name, t.data as data
			from %s t
			where t.name = %s
				and case
					when %s is not null and %s <> '' then t.tenant = %s and t.project::text = coalesce(%s, '')
					else t.tenant = %s.tenant or t.tenant = 'shared'
				end
			order by (t.tenant = %s.tenant) desc, t.project::text
			limit 1
		) %s on true
		`,
		table, nameExpr, tenantExpr, tenantExpr, tenantExpr, projectExpr, row, row, alias,
	)
}

// referenceExprs reads a stored reference's name, tenant and project from a table row.
func referenceExprs(row, path string) (name, tenant, project string) {
	return rowPath(row, path+".name"), rowPath(row, path+".tenant"), rowPath(row, path+".project")
}

// itemReferenceExprs reads a stored reference's name, tenant and project from a jsonb item.
func itemReferenceExprs(item, path string) (name, tenant, project string) {
	return itemPath(item, path+".name"), itemPath(item, path+".tenant"), itemPath(item, path+".project")
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
			name, tenant, project := itemReferenceExprs(g.item, source)
			alias := g.nextAlias("t")
			from += g.typeJoin(createdField.Through, name, tenant, project, "r", alias)
			// A planned child whose type resolves to nothing would ask for zero of its
			// dimensions, so the sentinel makes the count fail loudly instead:
			g.asks = append(g.asks, missingTypeAsk(name, alias, from))
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
	// kind. A link is trusted only for rows the system created, and the rows stop counting in the
	// states their kind's rules map, so a released child no longer nets against its parent's ask.
	// Several creates fields can name the same kind; the linked rows are scanned once per kind,
	// not once per field.
	if g.existsKinds[creates.Kind] {
		return nil
	}
	g.existsKinds[creates.Kind] = true
	row := g.nextAlias("v")
	from := fmt.Sprintf("%s %s", tables[created.Message], row)
	var prices []string
	for _, price := range rowPrice(created, row) {
		prices = append(prices, selectPrice(price.dimension, price.amount))
	}
	for _, createdField := range created.Fields {
		if createdField.Through == "" {
			continue
		}
		name, tenant, project := referenceExprs(row, createdField.Path)
		alias := g.nextAlias("t")
		from += g.typeJoin(createdField.Through, name, tenant, project, row, alias)
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
			%s
		`,
		from, union(prices), row, row, link, g.stopsCountingIn(creates.Kind, row),
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
