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
// deletion timestamp.
package quotausage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

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
	logger *slog.Logger
	kinds  map[string]*quota.Kind
}

// CounterBuilder contains the data and logic needed to create a counter.
type CounterBuilder struct {
	logger *slog.Logger
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

// Build creates the counter. It fails loudly when the registry and the tables map disagree, so a
// new annotated kind without a table is a startup failure, not a silently uncounted resource.
func (b *CounterBuilder) Build() (result *Counter, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	kinds, err := quota.Kinds()
	if err != nil {
		return nil, fmt.Errorf("failed to load the quota registry: %w", err)
	}
	result = &Counter{logger: b.logger, kinds: map[string]*quota.Kind{}}
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
			if _, _, err := findLink(created, kind.Name); err != nil {
				return fmt.Errorf("kind '%s': %w", kind.Name, err)
			}
		}
	}
	return nil
}

// findLink returns how the created kind points back to the parent kind: the created-for options
// and, for a field link, the path of the parent reference.
func findLink(created *quota.Kind, parent string) (*quota.CreatedFor, string, error) {
	if created.CreatedFor != nil &&
		(created.CreatedFor.Parent == parent || created.CreatedFor.IDLabel != "") {
		return created.CreatedFor, "", nil
	}
	for _, field := range created.Fields {
		if field.CreatedFor != nil && field.CreatedFor.Parent == parent {
			return field.CreatedFor, field.Path, nil
		}
	}
	return nil, "", fmt.Errorf(
		"created kind '%s' has no created_for link to parent kind '%s'", created.Name, parent,
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
	for _, name := range sortedKindNames(c.kinds) {
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

func sortedKindNames(kinds map[string]*quota.Kind) []string {
	names := make([]string, 0, len(kinds))
	for name := range kinds {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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
			group by r.project, u.dimension
			`,
			g.table, joinClauses(g.rowJoins), union(g.asks),
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
		g.table, joinClauses(g.rowJoins), union(g.asks), union(g.exists),
	), nil
}

// addThrough prices a reference that resolves through a type: one join per reference, and the
// larger amount per dimension when the annotation names a second, running reference.
func (g *statementGenerator) addThrough(field quota.Field) error {
	typeKind := g.counter.kinds[field.Through]
	asked := g.nextAlias("t")
	g.rowJoins = append(g.rowJoins, g.typeJoin(
		field.Through, rowPath("r", field.Path+".name"), "r", asked,
	))
	askedPrice := typePrice(typeKind, asked)
	runningPrice := []pricePair{}
	if field.LargerOf != "" {
		running := g.nextAlias("t")
		g.rowJoins = append(g.rowJoins, g.typeJoin(
			field.Through, rowPath("r", field.LargerOf+".name"), "r", running,
		))
		runningPrice = typePrice(typeKind, running)
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

// typeJoin joins one row of the type's table, matched by the reference's name, preferring a type
// of the resource's own tenant over the shared tenant. The join picks at most one row, so a name
// that exists in both scopes can't double the price.
func (g *statementGenerator) typeJoin(typeKind, nameExpr, row, alias string) string {
	table := tables[g.counter.kinds[typeKind].Message]
	return fmt.Sprintf(`
		left join lateral (
			select t.name as name, t.data as data
			from %s t
			where t.name = %s
				and (t.tenant = %s.tenant or t.tenant = 'shared')
			order by (t.tenant = %s.tenant) desc
			limit 1
		) %s on true
		`,
		table, nameExpr, row, row, alias,
	)
}

// typePrice prices one type row at the given alias: a type's own dimensions, which are a message
// dimension and field dimensions. checkKinds refused any other shape.
func typePrice(kind *quota.Kind, alias string) (result []pricePair) {
	if kind.Dimension != "" {
		result = append(result, pricePair{dimension: rowDimension(kind.Dimension, alias), amount: "1"})
	}
	for _, field := range kind.Fields {
		result = append(result, pricePair{
			dimension: rowDimension(field.Dimension, alias),
			amount:    intCast(rowPath(alias, field.Path)),
		})
	}
	return result
}

// rowPrice prices one stored row of a kind at the given alias: its message dimension and its field
// dimensions. Through references need joins and are priced by the caller.
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
	createdFor, linkField, err := findLink(created, g.kind.Name)
	if err != nil {
		return err
	}

	descriptor, err := fieldDescriptor(g.kind, field.Path)
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
			alias := g.nextAlias("t")
			from += g.typeJoin(
				createdField.Through, itemPath(g.item, source+".name"), "r", alias,
			)
			for _, price := range typePrice(g.counter.kinds[createdField.Through], alias) {
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
	from := fmt.Sprintf("%s %s", tables[created.Message], row)
	var prices []string
	for _, price := range rowPrice(created, row) {
		prices = append(prices, selectPrice(price.dimension, price.amount))
	}
	for _, createdField := range created.Fields {
		if createdField.Through == "" {
			continue
		}
		alias := g.nextAlias("t")
		from += g.typeJoin(createdField.Through, rowPath(row, createdField.Path+".name"), row, alias)
		for _, price := range typePrice(g.counter.kinds[createdField.Through], alias) {
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
		return fmt.Sprintf("starts_with(%s.name, %s)", row, strings.Join(exprs, " || ")), nil
	case createdFor.IDLabel != "":
		return fmt.Sprintf(
			"%s.labels ->> %s = %s.id and %s.labels ->> %s = '%s'",
			row, sqlString(createdFor.IDLabel), parent,
			row, sqlString(createdFor.KindLabel), parentKind,
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

// fieldDescriptor resolves the descriptor of an annotated field, so the generator can tell a bool,
// a list, a map and a message apart without naming any kind.
func fieldDescriptor(kind *quota.Kind, path string) (protoreflect.FieldDescriptor, error) {
	descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(kind.Message)
	if err != nil {
		return nil, err
	}
	message, ok := descriptor.(protoreflect.MessageDescriptor)
	if !ok {
		return nil, fmt.Errorf("kind message '%s' isn't a message", kind.Message)
	}
	current := message
	segments := strings.Split(path, ".")
	for i, segment := range segments {
		field := current.Fields().ByName(protoreflect.Name(segment))
		if field == nil {
			return nil, fmt.Errorf("message '%s' has no field '%s'", current.FullName(), segment)
		}
		if i == len(segments)-1 {
			return field, nil
		}
		current = field.Message()
	}
	return nil, fmt.Errorf("empty field path")
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

func joinClauses(clauses []string) string {
	if len(clauses) == 0 {
		return ""
	}
	return strings.Join(clauses, "\n")
}

func union(selects []string) string {
	return strings.Join(selects, "\nunion all\n")
}
