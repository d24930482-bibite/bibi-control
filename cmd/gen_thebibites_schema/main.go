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
	Field   string
	Column  string
	SQLType string
}

type rowField struct {
	Name string
	Type ast.Expr
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
	if err := os.WriteFile(filepath.Join(root, "duckdb", "migrations", "0001_extracted_save.sql"), []byte(renderMigration(tables)), 0o644); err != nil {
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

	structs := make(map[string][]rowField)
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
			structs[typeSpec.Name.Name] = parseStructFields(structType)
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
		rowFields, ok := structs[rowType]
		if !ok {
			return nil, fmt.Errorf("row type %s for table %s not found", rowType, tableName)
		}
		table := tableSpec{
			SaveField: astField.Names[0].Name,
			Table:     tableName,
			RowType:   rowType,
			Optional:  optional,
			Fields:    make([]field, 0, len(rowFields)),
		}
		for _, rowField := range rowFields {
			sqlType, err := sqlType(rowField.Type)
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", rowType, rowField.Name, err)
			}
			table.Fields = append(table.Fields, field{
				Field:   rowField.Name,
				Column:  columnName(rowType, rowField.Name),
				SQLType: sqlType,
			})
		}
		tables = append(tables, table)
	}
	return tables, nil
}

func parseStructFields(structType *ast.StructType) []rowField {
	fields := make([]rowField, 0, len(structType.Fields.List))
	for _, astField := range structType.Fields.List {
		for _, name := range astField.Names {
			fields = append(fields, rowField{Name: name.Name, Type: astField.Type})
		}
	}
	return fields
}

func tableNameFromTag(tag *ast.BasicLit) string {
	if tag == nil {
		return ""
	}
	return reflect.StructTag(strings.Trim(tag.Value, "`")).Get("dbtable")
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
		case "int", "int8", "int16", "int32":
			return "INTEGER", nil
		case "int64", "uint", "uint8", "uint16", "uint32", "uint64":
			return "BIGINT", nil
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

func renderMigration(tables []tableSpec) string {
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
	b.WriteString(strings.TrimSpace(customSQLTrailer))
	b.WriteString("\n")
	return b.String()
}

const customSQLTrailer = `
CREATE OR REPLACE VIEW bibite_mutation_refs AS
SELECT save_id, entry_name, body_id, health, energy, dead, dying, has_body_id
FROM bibites
WHERE has_body_id;
`

func writeFormattedGo(path string, source []byte) error {
	formatted, err := format.Source(source)
	if err != nil {
		return fmt.Errorf("format %s: %w\n%s", path, err, source)
	}
	return os.WriteFile(path, formatted, 0o644)
}
