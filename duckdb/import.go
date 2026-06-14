// Package duckdb loads normalized save projections into DuckDB for analytics.
package duckdb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"sort"
	"strings"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"

	duckdbgo "github.com/duckdb/duckdb-go/v2"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Open opens a DuckDB database. Pass an empty path for an in-memory database.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("duckdb", path)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// OpenAndImport opens a DuckDB database, applies migrations, and imports save.
// The caller owns the returned database handle.
func OpenAndImport(ctx context.Context, path string, save tb.ExtractedSave) (*sql.DB, error) {
	db, err := Open(path)
	if err != nil {
		return nil, err
	}
	if err := ImportExtractedSave(ctx, db, save); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// ApplyMigrations creates the DuckDB schema needed for ExtractedSave imports.
func ApplyMigrations(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("duckdb: db is nil")
	}

	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		path := "migrations/" + entry.Name()
		sqlBytes, err := migrationFiles.ReadFile(path)
		if err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, string(sqlBytes)); err != nil {
			return fmt.Errorf("duckdb migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}

// ImportExtractedSave applies migrations and replaces any existing rows with
// the same save_id before inserting every row family in save.
func ImportExtractedSave(ctx context.Context, db *sql.DB, save tb.ExtractedSave) error {
	if err := ApplyMigrations(ctx, db); err != nil {
		return err
	}
	return ReplaceExtractedSave(ctx, db, save)
}

// ReplaceExtractedSave replaces rows for save.Archive.SaveID with save's
// normalized table rows. Archive entry bytes remain outside DuckDB.
func ReplaceExtractedSave(ctx context.Context, db *sql.DB, save tb.ExtractedSave) error {
	if db == nil {
		return fmt.Errorf("duckdb: db is nil")
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN TRANSACTION"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	if err := deleteSaveRows(ctx, conn, save.Archive.SaveID); err != nil {
		return err
	}
	if err := insertExtractedSave(ctx, conn, save); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

type execContext interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func deleteSaveRows(ctx context.Context, execer execContext, saveID string) error {
	for _, table := range allTables {
		query := fmt.Sprintf("DELETE FROM %s WHERE save_id = ?", quoteIdent(table))
		if _, err := execer.ExecContext(ctx, query, saveID); err != nil {
			return fmt.Errorf("delete %s rows for save_id %q: %w", table, saveID, err)
		}
	}
	return nil
}

func insertExtractedSave(ctx context.Context, conn *sql.Conn, save tb.ExtractedSave) error {
	if err := insertStruct(ctx, conn, "save_archives", saveArchiveFields, save.Archive); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "save_entries", saveEntryFields, save.Entries); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "diagnostics", diagnosticFields, save.Diagnostics); err != nil {
		return err
	}
	if err := insertOptional(ctx, conn, "scenes", sceneFields, save.Scene); err != nil {
		return err
	}
	if err := insertOptional(ctx, conn, "vars", varsFields, save.Vars); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "scene_color_selectors", sceneColorSelectorFields, save.SceneColorSelectors); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "scene_phero_towers", sceneTowerFields, save.ScenePheroTowers); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "scene_rad_towers", sceneTowerFields, save.SceneRadTowers); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "settings_simulation_values", settingValueFields, save.SettingsSimulationValues); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "settings_independent_values", settingValueFields, save.SettingsIndependentValues); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "settings_materials", settingsMaterialFields, save.SettingsMaterials); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "settings_material_values", settingValueFields, save.SettingsMaterialValues); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "settings_zones", settingsZoneFields, save.SettingsZones); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "settings_zone_geometry", settingsZoneGeometryFields, save.SettingsZoneGeometry); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "settings_zone_values", settingValueFields, save.SettingsZoneValues); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "settings_zone_groups", settingsZoneGroupFields, save.SettingsZoneGroups); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "settings_bibite_spawners", settingsBibiteSpawnerFields, save.SettingsBibiteSpawners); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "settings_changers", settingsChangerFields, save.SettingsChangers); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "settings_changer_points", settingsChangerPointFields, save.SettingsChangerPoints); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "settings_changer_targets", settingsChangerTargetFields, save.SettingsChangerTargets); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "active_species", activeSpeciesFields, save.ActiveSpecies); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "species", speciesFields, save.Species); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "species_genes", geneFields, save.SpeciesGenes); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "species_brain_nodes", brainNodeFields, save.SpeciesBrainNodes); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "species_brain_synapses", brainSynapseFields, save.SpeciesBrainSynapses); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "bibites", bibiteFields, save.Bibites); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "bibite_genes", geneFields, save.BibiteGenes); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "bibite_body", bibiteBodyFields, save.BibiteBody); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "bibite_mouth", bibiteMouthFields, save.BibiteMouth); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "bibite_pheromone_emitters", bibitePheromoneEmitterFields, save.BibitePheromoneEmitters); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "bibite_egg_layers", bibiteEggLayerFields, save.BibiteEggLayers); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "bibite_control", bibiteControlFields, save.BibiteControl); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "bibite_stomach_contents", stomachContentFields, save.BibiteStomachContents); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "bibite_children", bibiteChildFields, save.BibiteChildren); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "bibite_brain_nodes", brainNodeFields, save.BibiteBrainNodes); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "bibite_brain_synapses", brainSynapseFields, save.BibiteBrainSynapses); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "eggs", eggFields, save.Eggs); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "egg_genes", geneFields, save.EggGenes); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "egg_brain_nodes", brainNodeFields, save.EggBrainNodes); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "egg_brain_synapses", brainSynapseFields, save.EggBrainSynapses); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "pellet_groups", pelletGroupFields, save.PelletGroups); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "pellets", pelletFields, save.Pellets); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "pheromones", pheromoneFields, save.Pheromones); err != nil {
		return err
	}
	if err := insertSlice(ctx, conn, "json_scalars", scalarFields, save.JSONScalars); err != nil {
		return err
	}
	return nil
}

type fieldSpec struct {
	field  string
	column string
}

func insertStruct(ctx context.Context, conn *sql.Conn, table string, fields []fieldSpec, row any) error {
	rowValue := reflect.ValueOf(row)
	return appendRows(ctx, conn, table, fields, rowValue)
}

func insertOptional(ctx context.Context, conn *sql.Conn, table string, fields []fieldSpec, row any) error {
	rowValue := reflect.ValueOf(row)
	if !rowValue.IsValid() || rowValue.IsNil() {
		return nil
	}
	return appendRows(ctx, conn, table, fields, rowValue)
}

func insertSlice(ctx context.Context, conn *sql.Conn, table string, fields []fieldSpec, rows any) error {
	rowsValue := reflect.ValueOf(rows)
	if rowsValue.Kind() != reflect.Slice {
		return fmt.Errorf("duckdb: %s rows are %s, want slice", table, rowsValue.Kind())
	}
	if rowsValue.Len() == 0 {
		return nil
	}
	return appendRows(ctx, conn, table, fields, rowsValue)
}

func appendRows(ctx context.Context, conn *sql.Conn, table string, fields []fieldSpec, rows reflect.Value) error {
	if rows.Kind() != reflect.Slice {
		single := reflect.MakeSlice(reflect.SliceOf(rows.Type()), 1, 1)
		single.Index(0).Set(rows)
		rows = single
	}
	columns := fieldColumns(fields)
	return conn.Raw(func(rawConn any) error {
		driverConn, ok := rawConn.(driver.Conn)
		if !ok {
			return fmt.Errorf("duckdb: raw connection is %T, want driver.Conn", rawConn)
		}

		appender, err := duckdbgo.NewAppenderWithColumns(driverConn, "", "", table, columns)
		if err != nil {
			return fmt.Errorf("create appender %s: %w", table, err)
		}
		closed := false
		defer func() {
			if !closed {
				_ = appender.Clear()
				_ = appender.Close()
			}
		}()

		for i := 0; i < rows.Len(); i++ {
			values, err := rowValues(table, rows.Index(i), fields)
			if err != nil {
				return err
			}
			if err := appender.AppendRow(values...); err != nil {
				clearErr := appender.Clear()
				closeErr := appender.Close()
				closed = true
				return fmt.Errorf("append %s row %d: %w", table, i, errors.Join(err, clearErr, closeErr))
			}
		}
		if err := appender.CloseWithCancel(ctx); err != nil {
			closed = true
			return fmt.Errorf("close appender %s: %w", table, err)
		}
		closed = true
		return nil
	})
}

func fieldColumns(fields []fieldSpec) []string {
	columns := make([]string, len(fields))
	for i, field := range fields {
		columns[i] = field.column
	}
	return columns
}

func rowValues(table string, row reflect.Value, fields []fieldSpec) ([]driver.Value, error) {
	for row.Kind() == reflect.Pointer {
		if row.IsNil() {
			return nil, fmt.Errorf("duckdb: %s row is nil", table)
		}
		row = row.Elem()
	}
	if row.Kind() != reflect.Struct {
		return nil, fmt.Errorf("duckdb: %s row is %s, want struct", table, row.Kind())
	}

	values := make([]driver.Value, len(fields))
	for i, spec := range fields {
		field := row.FieldByName(spec.field)
		if !field.IsValid() {
			return nil, fmt.Errorf("duckdb: %s row missing field %s", table, spec.field)
		}
		value, err := fieldValue(field)
		if err != nil {
			return nil, fmt.Errorf("duckdb: %s.%s: %w", table, spec.field, err)
		}
		values[i] = value
	}
	return values, nil
}

func fieldValue(value reflect.Value) (driver.Value, error) {
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil, nil
		}
		value = value.Elem()
	}

	switch value.Kind() {
	case reflect.Bool:
		return value.Bool(), nil
	case reflect.String:
		return value.String(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if value.Uint() > maxDriverInt64 {
			return nil, fmt.Errorf("uint value %d overflows int64", value.Uint())
		}
		return int64(value.Uint()), nil
	case reflect.Float32, reflect.Float64:
		return value.Float(), nil
	default:
		return nil, fmt.Errorf("unsupported kind %s", value.Kind())
	}
}

func quoteIdent(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

const maxDriverInt64 = uint64(1<<63 - 1)
