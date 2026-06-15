package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"unicode"
)

type tableSpec struct {
	SaveField string
	Table     string
	RowType   string
	Optional  bool
	Fields    []field
}

type field struct {
	Field      string
	Column     string
	SQLType    string
	SQLRefPath string
}

type rowField struct {
	Name string
	Type ast.Expr
	Tag  *ast.BasicLit
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "gen_thebibites_schema: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	sourcePath := filepath.Join(root, "saveparser", "thebibites", "normalize_types.go")

	tables, err := parseNormalizedTables(sourcePath)
	if err != nil {
		return err
	}

	if err := writeFormattedGo(filepath.Join(root, "saveparser", "thebibites", "normalize_metadata.go"), renderParserMetadata(tables)); err != nil {
		return err
	}

	migration, err := renderMigration(tables)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "duckdb", "migrations", "0001_extracted_save.sql"), []byte(migration), 0o644); err != nil {
		return err
	}

	sqlRefs, err := renderSQLRefCatalog(tables)
	if err != nil {
		return err
	}
	if err := writeFormattedGo(filepath.Join(root, "savemutator", "thebibites", "sqlref_generated.go"), sqlRefs); err != nil {
		return err
	}
	return nil
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find go.mod from %s", dir)
		}
		dir = parent
	}
}

func parseNormalizedTables(path string) ([]tableSpec, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	// Store every declared struct as its raw AST node so embedded fields can be
	// resolved and flattened on demand (fix 2). ExtractedSave is handled
	// separately because its fields carry dbtable tags and container types.
	structs := make(map[string]*ast.StructType)
	var extracted *ast.StructType
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			if typeSpec.Name.Name == "ExtractedSave" {
				extracted = structType
				continue
			}
			structs[typeSpec.Name.Name] = structType
		}
	}
	if extracted == nil {
		return nil, fmt.Errorf("ExtractedSave struct not found")
	}

	tables := make([]tableSpec, 0, len(extracted.Fields.List))
	for _, astField := range extracted.Fields.List {
		if len(astField.Names) != 1 {
			return nil, fmt.Errorf("ExtractedSave field has %d names, want 1", len(astField.Names))
		}
		tableName := tableNameFromTag(astField.Tag)
		if tableName == "" {
			return nil, fmt.Errorf("ExtractedSave.%s is missing dbtable tag", astField.Names[0].Name)
		}
		rowType, optional, err := extractedRowType(astField.Type)
		if err != nil {
			return nil, fmt.Errorf("ExtractedSave.%s: %w", astField.Names[0].Name, err)
		}
		rowFields, err := flattenFields(rowType, structs, map[string]bool{})
		if err != nil {
			return nil, fmt.Errorf("table %s: %w", tableName, err)
		}
		table := tableSpec{
			SaveField: astField.Names[0].Name,
			Table:     tableName,
			RowType:   rowType,
			Optional:  optional,
			Fields:    make([]field, 0, len(rowFields)),
		}
		for _, rf := range rowFields {
			sqlType, err := sqlType(rf.Type)
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", rowType, rf.Name, err)
			}
			table.Fields = append(table.Fields, field{
				Field:      rf.Name,
				Column:     columnName(rowType, rf.Name),
				SQLType:    sqlType,
				SQLRefPath: sqlRefPathFromTag(rf.Tag),
			})
		}

		// Fix 3: a table with no columns emits invalid SQL (CREATE TABLE x ();)
		// that the .sql output never validates. Fail loudly at generate time.
		if len(table.Fields) == 0 {
			return nil, fmt.Errorf("table %s (%s) produced no columns", table.Table, rowType)
		}

		// Fix 3: two Go fields collapsing to one column (snakeCase edge case or a
		// copy-pasted override) emits a duplicate column DuckDB rejects at apply
		// time. Catch it here. The resulting set doubles as the column lookup for
		// the EAV discriminator check below.
		cols := make(map[string]bool, len(table.Fields))
		for _, f := range table.Fields {
			if cols[f.Column] {
				return nil, fmt.Errorf("table %s: duplicate column %q", table.Table, f.Column)
			}
			cols[f.Column] = true
		}

		// Fix 5: value-shaped (EAV) rows must name their discriminator value_type
		// so the uniform `WHERE value_type = '...'` predicate the ref/query layer
		// depends on can't silently break when a new value table forgets the
		// per-row override and snakeCase produces "type".
		if cols["number_value"] && cols["bool_value"] {
			if cols["type"] || !cols["value_type"] {
				return nil, fmt.Errorf("EAV table %s (%s): discriminator must map to column value_type", table.Table, rowType)
			}
		}

		tables = append(tables, table)
	}
	return tables, nil
}

// flattenFields resolves a row struct into a flat, ordered field list, expanding
// embedded structs in place. An embedded field contributes zero AST names, so the
// previous implementation silently dropped it; here an unresolved or cyclic embed
// is a hard error instead (fix 2).
func flattenFields(name string, structs map[string]*ast.StructType, stack map[string]bool) ([]rowField, error) {
	st, ok := structs[name]
	if !ok {
		return nil, fmt.Errorf("struct %s not found", name)
	}
	if stack[name] {
		return nil, fmt.Errorf("embedded cycle through %s", name)
	}
	stack[name] = true
	defer delete(stack, name)

	out := make([]rowField, 0, len(st.Fields.List))
	for _, f := range st.Fields.List {
		if len(f.Names) == 0 { // embedded field
			emb, err := embeddedTypeName(f.Type)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			inner, err := flattenFields(emb, structs, stack)
			if err != nil {
				return nil, err
			}
			out = append(out, inner...)
			continue
		}
		for _, n := range f.Names {
			out = append(out, rowField{Name: n.Name, Type: f.Type, Tag: f.Tag})
		}
	}
	return out, nil
}

func embeddedTypeName(expr ast.Expr) (string, error) {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name, nil
	case *ast.StarExpr:
		return embeddedTypeName(t.X)
	default:
		return "", fmt.Errorf("unsupported embedded field type %T", expr)
	}
}

func tableNameFromTag(tag *ast.BasicLit) string {
	if tag == nil {
		return ""
	}
	return reflect.StructTag(strings.Trim(tag.Value, "`")).Get("dbtable")
}

func sqlRefPathFromTag(tag *ast.BasicLit) string {
	if tag == nil {
		return ""
	}
	return reflect.StructTag(strings.Trim(tag.Value, "`")).Get("sqlref")
}

func extractedRowType(expr ast.Expr) (string, bool, error) {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name, false, nil
	case *ast.StarExpr:
		name, _, err := extractedRowType(t.X)
		return name, true, err
	case *ast.ArrayType:
		name, _, err := extractedRowType(t.Elt)
		return name, false, err
	default:
		return "", false, fmt.Errorf("unsupported row container %T", expr)
	}
}

func sqlType(expr ast.Expr) (string, error) {
	switch t := expr.(type) {
	case *ast.Ident:
		switch t.Name {
		case "string", "EntryKind", "DiagnosticSeverity", "ScalarType":
			return "TEXT", nil
		case "bool":
			return "BOOLEAN", nil
		case "float32", "float64":
			return "DOUBLE", nil
		// Fix 4: Go int is 64-bit on every real target; DuckDB INTEGER is signed
		// 32-bit, so int must map to BIGINT to avoid silent narrowing.
		case "int", "int64":
			return "BIGINT", nil
		case "int8", "int16", "int32":
			return "INTEGER", nil
		// Fix 4: unsigned Go types map to DuckDB's unsigned types. uint64 does not
		// fit signed BIGINT; UBIGINT is the faithful target. (Verify the DuckDB
		// driver round-trips UBIGINT/UINTEGER into Go uint64/uint on scan; if it
		// is fussy, BIGINT is a safe fallback for the only uint64 fields here,
		// which are entry byte sizes that cannot realistically overflow it.)
		case "uint", "uint64":
			return "UBIGINT", nil
		case "uint8", "uint16", "uint32":
			return "UINTEGER", nil
		default:
			return "", fmt.Errorf("unsupported field type %s", t.Name)
		}
	case *ast.StarExpr:
		return sqlType(t.X)
	default:
		return "", fmt.Errorf("unsupported field type expression %T", expr)
	}
}

func columnName(rowType, fieldName string) string {
	if overrides, ok := columnOverridesByRow[rowType]; ok {
		if column, ok := overrides[fieldName]; ok {
			return column
		}
	}
	if column, ok := columnOverrides[fieldName]; ok {
		return column
	}
	return snakeCase(fieldName)
}

var columnOverrides = map[string]string{
	"HasUTF8BOM": "has_utf8_bom",
	"RB2DPX":     "rb2d_px",
	"RB2DPY":     "rb2d_py",
	"RB2DVX":     "rb2d_vx",
	"RB2DVY":     "rb2d_vy",
	"RB2DR":      "rb2d_r",
}

var columnOverridesByRow = map[string]map[string]string{
	"BrainNodeRow": {
		"Type": "node_type",
		"Desc": "node_desc",
	},
	"GeneRow": {
		"Type": "value_type",
	},
	"ScalarRow": {
		"Type": "value_type",
	},
	"SettingValueRow": {
		"Type": "value_type",
	},
	"SettingsChangerRow": {
		"Repeat": "repeat_enabled",
		"Start":  "start_time",
	},
	"SettingsChangerTargetRow": {
		"Type": "value_type",
	},
}

func snakeCase(name string) string {
	if name == "" {
		return ""
	}
	runes := []rune(name)
	words := make([]string, 0, 4)
	start := 0
	for i := 1; i < len(runes); i++ {
		prev := runes[i-1]
		curr := runes[i]
		var next rune
		if i+1 < len(runes) {
			next = runes[i+1]
		}
		if isBoundary(prev, curr, next) {
			words = append(words, string(runes[start:i]))
			start = i
		}
	}
	words = append(words, string(runes[start:]))
	for i, word := range words {
		words[i] = strings.ToLower(word)
	}
	return strings.Join(words, "_")
}

func isBoundary(prev, curr, next rune) bool {
	if unicode.IsLower(prev) && unicode.IsUpper(curr) {
		return true
	}
	if unicode.IsDigit(prev) && unicode.IsUpper(curr) && next != 0 && unicode.IsLower(next) {
		return true
	}
	if unicode.IsUpper(prev) && unicode.IsUpper(curr) && next != 0 && unicode.IsLower(next) {
		return true
	}
	return false
}

func renderParserMetadata(tables []tableSpec) []byte {
	var b bytes.Buffer
	b.WriteString("// Code generated by go generate ./saveparser/thebibites; DO NOT EDIT.\n\n")
	b.WriteString("package thebibites\n\n")
	b.WriteString("type NormalizedTableSpec struct {\n")
	b.WriteString("\tSaveField string\n")
	b.WriteString("\tTable string\n")
	b.WriteString("\tRowType string\n")
	b.WriteString("\tOptional bool\n")
	b.WriteString("\tFields []NormalizedFieldSpec\n")
	b.WriteString("}\n\n")
	b.WriteString("type NormalizedFieldSpec struct {\n")
	b.WriteString("\tField string\n")
	b.WriteString("\tColumn string\n")
	b.WriteString("\tSQLType string\n")
	b.WriteString("}\n\n")
	b.WriteString("var NormalizedTables = []NormalizedTableSpec{\n")
	for _, table := range tables {
		fmt.Fprintf(&b, "\t{SaveField: %q, Table: %q, RowType: %q, Optional: %t, Fields: []NormalizedFieldSpec{\n", table.SaveField, table.Table, table.RowType, table.Optional)
		for _, field := range table.Fields {
			fmt.Fprintf(&b, "\t\t{Field: %q, Column: %q, SQLType: %q},\n", field.Field, field.Column, field.SQLType)
		}
		b.WriteString("\t}},\n")
	}
	b.WriteString("}\n")
	return b.Bytes()
}

type sqlRefPathMapSpec struct {
	RowType string
	VarName string
}

var sqlRefPathMapSpecs = []sqlRefPathMapSpec{
	{RowType: "BibiteRow", VarName: "bibiteColumnPaths"},
	{RowType: "BibiteBodyRow", VarName: "bibiteBodyColumnPaths"},
	{RowType: "BibiteMouthRow", VarName: "bibiteMouthColumnPaths"},
	{RowType: "BibitePheromoneEmitterRow", VarName: "bibitePheromoneColumnPaths"},
	{RowType: "BibiteEggLayerRow", VarName: "bibiteEggLayerColumnPaths"},
	{RowType: "BibiteControlRow", VarName: "bibiteControlColumnPaths"},
	{RowType: "EggRow", VarName: "eggColumnPaths"},
	{RowType: "BrainNodeRow", VarName: "brainNodeColumnKeys"},
	{RowType: "BrainSynapseRow", VarName: "brainSynapseColumnKeys"},
	{RowType: "StomachContentRow", VarName: "bibiteStomachContentColumnFields"},
	{RowType: "PelletRow", VarName: "pelletColumnPaths"},
	{RowType: "PheromoneRow", VarName: "pheromoneColumnPaths"},
	{RowType: "SettingsZoneRow", VarName: "settingsZoneColumnPaths"},
}

func renderSQLRefCatalog(tables []tableSpec) ([]byte, error) {
	fieldsByRow := make(map[string][]field, len(tables))
	for _, table := range tables {
		if _, ok := fieldsByRow[table.RowType]; !ok {
			fieldsByRow[table.RowType] = table.Fields
		}
	}
	generatedRows := make(map[string]string, len(sqlRefPathMapSpecs))
	for _, spec := range sqlRefPathMapSpecs {
		if existing, ok := generatedRows[spec.RowType]; ok {
			return nil, fmt.Errorf("sqlref row type %s is configured for both %s and %s", spec.RowType, existing, spec.VarName)
		}
		generatedRows[spec.RowType] = spec.VarName
	}
	for _, table := range tables {
		if _, ok := generatedRows[table.RowType]; ok {
			continue
		}
		for _, field := range table.Fields {
			if field.SQLRefPath != "" {
				return nil, fmt.Errorf("%s.%s has sqlref tag but row type %s has no generated sqlref map", table.RowType, field.Field, table.RowType)
			}
		}
	}

	var b bytes.Buffer
	b.WriteString("// Code generated by go generate ./saveparser/thebibites; DO NOT EDIT.\n\n")
	b.WriteString("package thebibites\n\n")
	for _, spec := range sqlRefPathMapSpecs {
		fields, ok := fieldsByRow[spec.RowType]
		if !ok {
			return nil, fmt.Errorf("sqlref map %s references unknown row type %s", spec.VarName, spec.RowType)
		}
		tagged := make([]field, 0, len(fields))
		for _, field := range fields {
			if field.SQLRefPath != "" {
				tagged = append(tagged, field)
			}
		}
		if len(tagged) == 0 {
			return nil, fmt.Errorf("sqlref map %s (%s) has no sqlref-tagged fields", spec.VarName, spec.RowType)
		}
		fmt.Fprintf(&b, "var %s = map[string]string{\n", spec.VarName)
		for _, field := range tagged {
			fmt.Fprintf(&b, "\t%q: %q,\n", field.Column, field.SQLRefPath)
		}
		b.WriteString("}\n\n")
	}
	return b.Bytes(), nil
}

func renderMigration(tables []tableSpec) (string, error) {
	var b strings.Builder
	b.WriteString("-- Code generated by go generate ./saveparser/thebibites; DO NOT EDIT.\n\n")
	for _, table := range tables {
		fmt.Fprintf(&b, "CREATE TABLE IF NOT EXISTS %s (\n", table.Table)
		for i, field := range table.Fields {
			suffix := ","
			if i == len(table.Fields)-1 {
				suffix = ""
			}
			fmt.Fprintf(&b, "\t%s %s%s\n", field.Column, field.SQLType, suffix)
		}
		b.WriteString(");\n\n")
	}

	views, err := renderViews(tables)
	if err != nil {
		return "", err
	}
	b.WriteString(strings.TrimSpace(views))
	b.WriteString("\n")
	return b.String(), nil
}

// viewSpec declares a generated view in terms of the table and columns it reads,
// so the column list has a single home and can be checked against the generated
// schema (fix 1). Previously this lived as a hand-typed SQL string constant whose
// column references silently rotted when a struct field was renamed, failing only
// at DuckDB-apply time in another package.
type viewSpec struct {
	Name    string
	Table   string
	Columns []string
	Where   string
}

var generatedViews = []viewSpec{
	{
		Name:    "bibite_mutation_refs",
		Table:   "bibites",
		Columns: []string{"save_id", "entry_name", "body_id", "health", "energy", "dead", "dying", "has_body_id"},
		Where:   "has_body_id",
	},
}

func renderViews(tables []tableSpec) (string, error) {
	columnsByTable := make(map[string]map[string]bool, len(tables))
	for _, t := range tables {
		set := make(map[string]bool, len(t.Fields))
		for _, f := range t.Fields {
			set[f.Column] = true
		}
		columnsByTable[t.Table] = set
	}

	var b strings.Builder
	for _, v := range generatedViews {
		cols, ok := columnsByTable[v.Table]
		if !ok {
			return "", fmt.Errorf("view %s references unknown table %s", v.Name, v.Table)
		}
		for _, c := range v.Columns {
			if !cols[c] {
				return "", fmt.Errorf("view %s references %s.%s, which the schema no longer has", v.Name, v.Table, c)
			}
		}
		fmt.Fprintf(&b, "CREATE OR REPLACE VIEW %s AS\nSELECT %s\nFROM %s\nWHERE %s;\n\n",
			v.Name, strings.Join(v.Columns, ", "), v.Table, v.Where)
	}
	return b.String(), nil
}

func writeFormattedGo(path string, source []byte) error {
	formatted, err := format.Source(source)
	if err != nil {
		return fmt.Errorf("format %s: %w\n%s", path, err, source)
	}
	return os.WriteFile(path, formatted, 0o644)
}
