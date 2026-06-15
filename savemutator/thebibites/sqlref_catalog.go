package thebibites

import (
	"fmt"
	"sort"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

type sqlRefResolver func(SQLValueRef) (Target, string, error)

type sqlRefTableSpec struct {
	table   string
	columns map[string]string
	resolve sqlRefResolver
}

var writableSQLRefTables = generatedWritableSQLRefTables

func generatedSQLRefTable(table string, columns map[string]string, resolver tb.SQLRefResolverKind) sqlRefTableSpec {
	switch resolver {
	case tb.SQLRefResolverBibitePathMap:
		return pathMapSQLRefTable(table, columns, bibiteTargetFromSQLRef)
	case tb.SQLRefResolverEggPathMap:
		return pathMapSQLRefTable(table, columns, eggTargetFromSQLRef)
	case tb.SQLRefResolverBibiteStomachContentPathMap:
		return sqlRefTableSpec{table: table, columns: columns, resolve: bibiteStomachColumnResolver(columns)}
	case tb.SQLRefResolverBibiteBrainNodePathMap:
		return sqlRefTableSpec{table: table, columns: columns, resolve: brainNodeColumnResolver(tb.EntryBibite, columns)}
	case tb.SQLRefResolverBibiteBrainSynapsePathMap:
		return sqlRefTableSpec{table: table, columns: columns, resolve: brainSynapseColumnResolver(tb.EntryBibite, columns)}
	case tb.SQLRefResolverEggBrainNodePathMap:
		return sqlRefTableSpec{table: table, columns: columns, resolve: brainNodeColumnResolver(tb.EntryEgg, columns)}
	case tb.SQLRefResolverEggBrainSynapsePathMap:
		return sqlRefTableSpec{table: table, columns: columns, resolve: brainSynapseColumnResolver(tb.EntryEgg, columns)}
	case tb.SQLRefResolverPelletPathMap:
		return sqlRefTableSpec{table: table, columns: columns, resolve: pelletColumnResolver(columns)}
	case tb.SQLRefResolverPheromonePathMap:
		return sqlRefTableSpec{table: table, columns: columns, resolve: pheromoneColumnResolver(columns)}
	case tb.SQLRefResolverSettingsZonePathMap:
		return sqlRefTableSpec{table: table, columns: columns, resolve: settingsZoneColumnResolver(columns)}
	case tb.SQLRefResolverBibiteGeneValue:
		return sqlRefTableSpec{table: table, columns: columns, resolve: geneColumnResolver(tb.EntryBibite, columns)}
	case tb.SQLRefResolverEggGeneValue:
		return sqlRefTableSpec{table: table, columns: columns, resolve: geneColumnResolver(tb.EntryEgg, columns)}
	case tb.SQLRefResolverSettingsValue:
		return sqlRefTableSpec{table: table, columns: columns, resolve: settingsValueColumnResolver(columns)}
	case tb.SQLRefResolverSettingsChangerTarget:
		return sqlRefTableSpec{table: table, columns: columns, resolve: settingsChangerTargetColumnResolver(columns)}
	default:
		panic(fmt.Sprintf("unsupported generated SQL ref resolver %q for table %q", resolver, table))
	}
}

func pathMapSQLRefTable(table string, columns map[string]string, targetResolver sqlRefTargetResolver) sqlRefTableSpec {
	return sqlRefTableSpec{table: table, columns: columns, resolve: pathMapResolver(columns, targetResolver)}
}

func geneColumnResolver(kind tb.EntryKind, columns map[string]string) sqlRefResolver {
	return func(ref SQLValueRef) (Target, string, error) {
		return resolveGeneColumn(ref, kind, columns)
	}
}

func writableSQLRefTable(table string) (sqlRefTableSpec, bool) {
	for _, spec := range writableSQLRefTables {
		if spec.table == table {
			return spec, true
		}
	}
	return sqlRefTableSpec{}, false
}

func writableSQLRefKeys() []string {
	var keys []string
	for _, spec := range writableSQLRefTables {
		for _, column := range sortedSQLRefColumns(spec.columns) {
			keys = append(keys, spec.table+"."+column)
		}
	}
	sort.Strings(keys)
	return keys
}

func sortedSQLRefColumns(columns map[string]string) []string {
	keys := make([]string, 0, len(columns))
	for column := range columns {
		keys = append(keys, column)
	}
	sort.Strings(keys)
	return keys
}

func unsupportedSQLValueRef(ref SQLValueRef) error {
	return fmt.Errorf("sql value ref %s.%s is not writable", ref.Table, ref.Column)
}
