package thebibites

import (
	"fmt"
	"sort"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

var settingsValueColumnTypes = map[string]string{
	"number_value": string(tb.ScalarNumber),
	"string_value": string(tb.ScalarString),
	"bool_value":   string(tb.ScalarBool),
}

var settingsChangerTargetColumns = map[string]string{
	"number_value": string(tb.ScalarNumber),
}

type sqlRefResolver func(SQLValueRef) (Target, string, error)

type sqlRefTableSpec struct {
	table   string
	columns map[string]string
	resolve sqlRefResolver
}

var writableSQLRefTables = []sqlRefTableSpec{
	pathMapSQLRefTable("bibites", bibiteColumnPaths, bibiteTargetFromSQLRef),
	pathMapSQLRefTable("bibite_body", bibiteBodyColumnPaths, bibiteTargetFromSQLRef),
	pathMapSQLRefTable("bibite_mouth", bibiteMouthColumnPaths, bibiteTargetFromSQLRef),
	pathMapSQLRefTable("bibite_pheromone_emitters", bibitePheromoneColumnPaths, bibiteTargetFromSQLRef),
	pathMapSQLRefTable("bibite_egg_layers", bibiteEggLayerColumnPaths, bibiteTargetFromSQLRef),
	pathMapSQLRefTable("bibite_control", bibiteControlColumnPaths, bibiteTargetFromSQLRef),
	{table: "bibite_stomach_contents", columns: bibiteStomachContentColumnFields, resolve: resolveBibiteStomachColumn},
	{table: "bibite_genes", columns: settingsValueColumnTypes, resolve: geneColumnResolver(tb.EntryBibite)},
	{table: "bibite_brain_nodes", columns: brainNodeColumnKeys, resolve: brainNodeColumnResolver(tb.EntryBibite)},
	{table: "bibite_brain_synapses", columns: brainSynapseColumnKeys, resolve: brainSynapseColumnResolver(tb.EntryBibite)},
	pathMapSQLRefTable("eggs", eggColumnPaths, eggTargetFromSQLRef),
	{table: "egg_genes", columns: settingsValueColumnTypes, resolve: geneColumnResolver(tb.EntryEgg)},
	{table: "egg_brain_nodes", columns: brainNodeColumnKeys, resolve: brainNodeColumnResolver(tb.EntryEgg)},
	{table: "egg_brain_synapses", columns: brainSynapseColumnKeys, resolve: brainSynapseColumnResolver(tb.EntryEgg)},
	{table: "pellets", columns: pelletColumnPaths, resolve: resolvePelletColumn},
	{table: "pheromones", columns: pheromoneColumnPaths, resolve: resolvePheromoneColumn},
	{table: "settings_simulation_values", columns: settingsValueColumnTypes, resolve: resolveSettingsValueColumn},
	{table: "settings_independent_values", columns: settingsValueColumnTypes, resolve: resolveSettingsValueColumn},
	{table: "settings_material_values", columns: settingsValueColumnTypes, resolve: resolveSettingsValueColumn},
	{table: "settings_zone_values", columns: settingsValueColumnTypes, resolve: resolveSettingsValueColumn},
	{table: "settings_changer_targets", columns: settingsChangerTargetColumns, resolve: resolveSettingsChangerTargetColumn},
	{table: "settings_zones", columns: settingsZoneColumnPaths, resolve: resolveSettingsZoneColumn},
}

func pathMapSQLRefTable(table string, columns map[string]string, targetResolver sqlRefTargetResolver) sqlRefTableSpec {
	return sqlRefTableSpec{table: table, columns: columns, resolve: pathMapResolver(columns, targetResolver)}
}

func geneColumnResolver(kind tb.EntryKind) sqlRefResolver {
	return func(ref SQLValueRef) (Target, string, error) {
		return resolveGeneColumn(ref, kind)
	}
}

func brainNodeColumnResolver(kind tb.EntryKind) sqlRefResolver {
	return func(ref SQLValueRef) (Target, string, error) {
		return resolveEntityBrainNodeColumn(ref, kind)
	}
}

func brainSynapseColumnResolver(kind tb.EntryKind) sqlRefResolver {
	return func(ref SQLValueRef) (Target, string, error) {
		return resolveEntityBrainSynapseColumn(ref, kind)
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
