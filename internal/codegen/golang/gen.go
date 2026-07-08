package golang

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/format"
	"slices"
	"strings"
	"text/template"

	"github.com/sqlc-dev/sqlc/internal/codegen/golang/opts"
	"github.com/sqlc-dev/sqlc/internal/codegen/sdk"
	"github.com/sqlc-dev/sqlc/internal/metadata"
	"github.com/sqlc-dev/sqlc/internal/plugin"
)

type tmplCtx struct {
	Q             string
	Package       string
	ModelsPackage string
	SQLDriver     opts.SQLDriver
	Enums         []Enum
	Structs       []Struct
	GoQueries     []Query
	SqlcVersion   string

	// TODO: Race conditions
	SourceName string

	EmitJSONTags              bool
	JsonTagsIDUppercase       bool
	EmitDBTags                bool
	EmitPreparedQueries       bool
	EmitInterface             bool
	EmitEmptySlices           bool
	EmitMethodsWithDBArgument bool
	EmitEnumValidMethod       bool
	EmitAllEnumValues         bool
	UsesCopyFrom              bool
	UsesBatch                 bool
	OmitSqlcVersion           bool
	BuildTags                 string
	WrapErrors                bool
}

func (t *tmplCtx) HasAnyStructWithIDField() bool {
	for _, s := range t.Structs {
		if s.HasIDField() {
			return true
		}
	}
	for _, q := range t.GoQueries {
		if q.Arg.EmitStruct() {
			for _, f := range q.Arg.UniqueFields() {
				if f.Name == "ID" {
					return true
				}
			}
		}
		if q.Ret.EmitStruct() {
			for _, f := range q.Ret.Struct.Fields {
				if f.Name == "ID" {
					return true
				}
			}
		}
	}
	return false
}


func (t *tmplCtx) OutputQuery(sourceName string) bool {
	return t.SourceName == sourceName
}

func (t *tmplCtx) codegenDbarg() string {
	if t.EmitMethodsWithDBArgument {
		return "db DBTX, "
	}
	return ""
}

// Called as a global method since subtemplate queryCodeStdExec does not have
// access to the toplevel tmplCtx
func (t *tmplCtx) codegenEmitPreparedQueries() bool {
	return t.EmitPreparedQueries
}

func (t *tmplCtx) codegenQueryMethod(q Query) string {
	db := "q.db"
	if t.EmitMethodsWithDBArgument {
		db = "db"
	}

	switch q.Cmd {
	case ":one":
		if t.EmitPreparedQueries {
			return "q.queryRow"
		}
		return db + ".QueryRowContext"

	case ":many":
		if t.EmitPreparedQueries {
			return "q.query"
		}
		return db + ".QueryContext"

	default:
		if t.EmitPreparedQueries {
			return "q.exec"
		}
		return db + ".ExecContext"
	}
}

func (t *tmplCtx) codegenQueryRetval(q Query) (string, error) {
	switch q.Cmd {
	case ":one":
		return "row :=", nil
	case ":many":
		return "rows, err :=", nil
	case ":exec":
		return "_, err :=", nil
	case ":execrows", ":execlastid":
		return "result, err :=", nil
	case ":execresult":
		if t.WrapErrors {
			return "result, err :=", nil
		}
		return "return", nil
	default:
		return "", fmt.Errorf("unhandled q.Cmd case %q", q.Cmd)
	}
}

func Generate(ctx context.Context, req *plugin.GenerateRequest) (*plugin.GenerateResponse, error) {
	options, err := opts.Parse(req)
	if err != nil {
		return nil, err
	}

	if err := opts.ValidateOpts(options); err != nil {
		return nil, err
	}

	enums := buildEnums(req, options)
	structs := buildStructs(req, options)
	queries, err := buildQueries(req, options, enums, structs)
	if err != nil {
		return nil, err
	}

	if options.OmitUnusedStructs {
		enums, structs = filterUnusedStructs(enums, structs, queries, options.ModelsTypeQualifier())
	}

	if err := validate(options, enums, structs, queries); err != nil {
		return nil, err
	}

	return generate(req, options, enums, structs, queries)
}

func validate(options *opts.Options, enums []Enum, structs []Struct, queries []Query) error {
	enumNames := make(map[string]struct{})
	for _, enum := range enums {
		enumNames[enum.Name] = struct{}{}
		enumNames["Null"+enum.Name] = struct{}{}
	}
	structNames := make(map[string]struct{})
	for _, struckt := range structs {
		if _, ok := enumNames[struckt.Name]; ok {
			return fmt.Errorf("struct name conflicts with enum name: %s", struckt.Name)
		}
		structNames[struckt.Name] = struct{}{}
	}
	if !options.EmitExportedQueries {
		return nil
	}
	for _, query := range queries {
		if _, ok := enumNames[query.ConstantName]; ok {
			return fmt.Errorf("query constant name conflicts with enum name: %s", query.ConstantName)
		}
		if _, ok := structNames[query.ConstantName]; ok {
			return fmt.Errorf("query constant name conflicts with struct name: %s", query.ConstantName)
		}
	}
	return nil
}

func generate(req *plugin.GenerateRequest, options *opts.Options, enums []Enum, structs []Struct, queries []Query) (*plugin.GenerateResponse, error) {
	i := &importer{
		Options: options,
		Queries: queries,
		Enums:   enums,
		Structs: structs,
	}

	tctx := tmplCtx{
		EmitInterface:             options.EmitInterface,
		EmitJSONTags:              options.EmitJsonTags,
		JsonTagsIDUppercase:       options.JsonTagsIdUppercase,
		EmitDBTags:                options.EmitDbTags,
		EmitPreparedQueries:       options.EmitPreparedQueries,
		EmitEmptySlices:           options.EmitEmptySlices,
		EmitMethodsWithDBArgument: options.EmitMethodsWithDbArgument,
		EmitEnumValidMethod:       options.EmitEnumValidMethod,
		EmitAllEnumValues:         options.EmitAllEnumValues,
		UsesCopyFrom:              usesCopyFrom(queries),
		UsesBatch:                 usesBatch(queries),
		SQLDriver:                 parseDriver(options.SqlPackage),
		Q:                         "`",
		Package:                   options.Package,
		ModelsPackage:             options.ModelsPackage(),
		Enums:                     enums,
		Structs:                   structs,
		SqlcVersion:               req.SqlcVersion,
		BuildTags:                 options.BuildTags,
		OmitSqlcVersion:           options.OmitSqlcVersion,
		WrapErrors:                options.WrapErrors,
	}

	if tctx.UsesCopyFrom && !tctx.SQLDriver.IsPGX() && options.SqlDriver != opts.SQLDriverGoSQLDriverMySQL {
		return nil, errors.New(":copyfrom is only supported by pgx and github.com/go-sql-driver/mysql")
	}

	if tctx.UsesCopyFrom && options.SqlDriver == opts.SQLDriverGoSQLDriverMySQL {
		if err := checkNoTimesForMySQLCopyFrom(queries); err != nil {
			return nil, err
		}
		tctx.SQLDriver = opts.SQLDriverGoSQLDriverMySQL
	}

	if tctx.UsesBatch && !tctx.SQLDriver.IsPGX() {
		return nil, errors.New(":batch* commands are only supported by pgx")
	}

	funcMap := template.FuncMap{
		"lowerTitle": sdk.LowerTitle,
		"comment":    sdk.DoubleSlashComment,
		"escape":     sdk.EscapeBacktick,
		"imports":    i.Imports,
		"hasImports": i.HasImports,
		"hasPrefix":  strings.HasPrefix,

		// These methods are Go specific, they do not belong in the codegen package
		// (as that is language independent)
		"dbarg":               tctx.codegenDbarg,
		"emitPreparedQueries": tctx.codegenEmitPreparedQueries,
		"queryMethod":         tctx.codegenQueryMethod,
		"queryRetval":         tctx.codegenQueryRetval,
		"loadRelations":       tctx.codegenLoadRelations,
	}

	tmpl := template.Must(
		template.New("table").
			Funcs(funcMap).
			ParseFS(
				templates,
				"templates/*.tmpl",
				"templates/*/*.tmpl",
			),
	)

	output := map[string]string{}

	execute := func(name, templateName string) error {
		imports := i.Imports(name)
		replacedQueries := replaceConflictedArg(imports, queries)

		var b bytes.Buffer
		w := bufio.NewWriter(&b)
		tctx.SourceName = name
		tctx.GoQueries = replacedQueries
		err := tmpl.ExecuteTemplate(w, templateName, &tctx)
		w.Flush()
		if err != nil {
			return err
		}
		code, err := format.Source(b.Bytes())
		if err != nil {
			fmt.Println(b.String())
			return fmt.Errorf("source error: %w", err)
		}

		if templateName == "queryFile" && options.OutputFilesSuffix != "" {
			name += options.OutputFilesSuffix
		}

		if !strings.HasSuffix(name, ".go") {
			name += ".go"
		}
		output[name] = string(code)
		return nil
	}

	dbFileName := "db.go"
	if options.OutputDbFileName != "" {
		dbFileName = options.OutputDbFileName
	}
	modelsFileName := "models.go"
	if options.OutputModelsFileName != "" {
		modelsFileName = options.OutputModelsFileName
	}
	querierFileName := "querier.go"
	if options.OutputQuerierFileName != "" {
		querierFileName = options.OutputQuerierFileName
	}
	copyfromFileName := "copyfrom.go"
	if options.OutputCopyfromFileName != "" {
		copyfromFileName = options.OutputCopyfromFileName
	}

	batchFileName := "batch.go"
	if options.OutputBatchFileName != "" {
		batchFileName = options.OutputBatchFileName
	}

	if err := execute(dbFileName, "dbFile"); err != nil {
		return nil, err
	}
	if options.ModelsEmitEnabled() {
		if err := execute(modelsFileName, "modelsFile"); err != nil {
			return nil, err
		}
	}
	if options.EmitInterface {
		if err := execute(querierFileName, "interfaceFile"); err != nil {
			return nil, err
		}
	}
	if tctx.UsesCopyFrom {
		if err := execute(copyfromFileName, "copyfromFile"); err != nil {
			return nil, err
		}
	}
	if tctx.UsesBatch {
		if err := execute(batchFileName, "batchFile"); err != nil {
			return nil, err
		}
	}

	files := map[string]struct{}{}
	for _, gq := range queries {
		files[gq.SourceName] = struct{}{}
	}

	for source := range files {
		if err := execute(source, "queryFile"); err != nil {
			return nil, err
		}
	}
	resp := plugin.GenerateResponse{}

	for filename, code := range output {
		resp.Files = append(resp.Files, &plugin.File{
			Name:     filename,
			Contents: []byte(code),
		})
	}

	return &resp, nil
}

func usesCopyFrom(queries []Query) bool {
	for _, q := range queries {
		if q.Cmd == metadata.CmdCopyFrom {
			return true
		}
	}
	return false
}

func usesBatch(queries []Query) bool {
	for _, q := range queries {
		if slices.Contains([]string{metadata.CmdBatchExec, metadata.CmdBatchMany, metadata.CmdBatchOne}, q.Cmd) {
			return true
		}
	}
	return false
}

func checkNoTimesForMySQLCopyFrom(queries []Query) error {
	for _, q := range queries {
		if q.Cmd != metadata.CmdCopyFrom {
			continue
		}
		for _, f := range q.Arg.CopyFromMySQLFields() {
			if f.Type == "time.Time" {
				return fmt.Errorf("values with a timezone are not yet supported")
			}
		}
	}
	return nil
}

func filterUnusedStructs(enums []Enum, structs []Struct, queries []Query, qualifier string) ([]Enum, []Struct) {
	keepTypes := make(map[string]struct{})

	keep := func(t string) {
		keepTypes[t] = struct{}{}
		// Also store the bare type name so that lookups against
		// bare struct/enum names match even when types have been
		// qualified with the models package prefix (e.g. "model.User").
		if bare := stripQualifier(t, qualifier); bare != t {
			keepTypes[bare] = struct{}{}
		}
	}

	for _, query := range queries {
		if !query.Arg.isEmpty() {
			keep(query.Arg.Type())
			if query.Arg.IsStruct() {
				for _, field := range query.Arg.Struct.Fields {
					keep(field.Type)
				}
			}
		}
		if query.hasRetType() {
			keep(query.Ret.Type())
			if query.Ret.IsStruct() {
				for _, field := range query.Ret.Struct.Fields {
					keep(strings.TrimPrefix(field.Type, "[]"))
					for _, embedField := range field.EmbedFields {
						keep(embedField.Type)
					}
				}
			}
		}
	}

	keepEnums := make([]Enum, 0, len(enums))
	for _, enum := range enums {
		_, keep := keepTypes[enum.Name]
		_, keepNull := keepTypes["Null"+enum.Name]
		_, keepPointer := keepTypes["*"+enum.Name]
		if keep || keepNull || keepPointer {
			keepEnums = append(keepEnums, enum)
		}
	}

	keepStructs := make([]Struct, 0, len(structs))
	for _, st := range structs {
		if _, ok := keepTypes[st.Name]; ok {
			keepStructs = append(keepStructs, st)
		}
	}

	return keepEnums, keepStructs
}

func (t *tmplCtx) codegenLoadRelations(q Query) string {
	if q.Ret.Struct == nil {
		return ""
	}
	var sb strings.Builder
	hasRels := false
	for _, f := range q.Ret.Struct.Fields {
		if f.IsRelation() {
			hasRels = true
			break
		}
	}
	if !hasRels {
		return ""
	}

	for _, f := range q.Ret.Struct.Fields {
		if !f.IsRelation() {
			continue
		}
		relName := f.RelationQueryName()
		var rq Query
		found := false
		for _, q_ := range t.GoQueries {
			if q_.MethodName == relName {
				rq = q_
				found = true
				break
			}
		}
		if !found {
			continue
		}

		pairs := rq.Arg.Pairs()
		if len(pairs) == 0 {
			continue
		}
		param := pairs[0]

		matchField, ok := FindMatchingField(q.Ret.Struct.Fields, param.Name)
		if !ok {
			continue
		}

		if q.Cmd == ":one" {
			sb.WriteString(fmt.Sprintf("\t// Load relation %s\n", f.Name))
			sb.WriteString("\t{\n")
			if matchField.Type != param.Type && !strings.HasPrefix(param.Type, "[]") {
				sb.WriteString(fmt.Sprintf("\t\tvar paramVal %s\n", param.Type))
				if matchField.Type == "interface{}" {
					sb.WriteString(fmt.Sprintf("\t\tswitch val := i.%s.(type) {\n", matchField.Name))
					sb.WriteString(fmt.Sprintf("\t\tcase %s:\n", param.Type))
					sb.WriteString("\t\t\tparamVal = val\n")
					if param.Type == "int64" {
						sb.WriteString("\t\tcase int:\n\t\t\tparamVal = int64(val)\n")
						sb.WriteString("\t\tcase int32:\n\t\t\tparamVal = int64(val)\n")
					}
					sb.WriteString("\t\t}\n")
				} else {
					sb.WriteString(fmt.Sprintf("\t\tparamVal = %s(i.%s)\n", param.Type, matchField.Name))
				}
				if rq.Arg.HasSqlcSlices() {
					sliceType := strings.TrimPrefix(param.Type, "[]")
					sb.WriteString(fmt.Sprintf("\t\trelVal, err := q.%s(ctx, []%s{paramVal})\n", relName, sliceType))
				} else {
					sb.WriteString(fmt.Sprintf("\t\trelVal, err := q.%s(ctx, paramVal)\n", relName))
				}
			} else {
				if rq.Arg.HasSqlcSlices() {
					sliceType := strings.TrimPrefix(param.Type, "[]")
					sb.WriteString(fmt.Sprintf("\t\trelVal, err := q.%s(ctx, []%s{i.%s})\n", relName, sliceType, matchField.Name))
				} else {
					sb.WriteString(fmt.Sprintf("\t\trelVal, err := q.%s(ctx, i.%s)\n", relName, matchField.Name))
				}
			}
			sb.WriteString("\t\tif err != nil {\n\t\t\treturn i, err\n\t\t}\n")
			sb.WriteString(fmt.Sprintf("\t\ti.%s = relVal\n", f.Name))
			sb.WriteString("\t}\n")
		} else if q.Cmd == ":many" {
			if rq.Arg.HasSqlcSlices() {
				// Batch loading! No N+1 queries!
				sb.WriteString(fmt.Sprintf("\t// Batch load relation %s to avoid N+1 queries\n", f.Name))
				sb.WriteString("\t{\n")
				sliceType := strings.TrimPrefix(param.Type, "[]")
				sb.WriteString(fmt.Sprintf("\t\trelIDs := make([]%s, 0, len(items))\n", sliceType))
				sb.WriteString("\t\tfor _, item := range items {\n")
				if matchField.Type == "interface{}" {
					sb.WriteString(fmt.Sprintf("\t\t\tswitch val := item.%s.(type) {\n", matchField.Name))
					sb.WriteString(fmt.Sprintf("\t\t\tcase %s:\n", sliceType))
					sb.WriteString("\t\t\t\trelIDs = append(relIDs, val)\n")
					if sliceType == "int64" {
						sb.WriteString("\t\t\tcase int:\n\t\t\t\trelIDs = append(relIDs, int64(val))\n")
						sb.WriteString("\t\t\tcase int32:\n\t\t\t\trelIDs = append(relIDs, int64(val))\n")
					}
					sb.WriteString("\t\t\t}\n")
				} else {
					sb.WriteString(fmt.Sprintf("\t\t\trelIDs = append(relIDs, %s(item.%s))\n", sliceType, matchField.Name))
				}
				sb.WriteString("\t\t}\n")
				sb.WriteString(fmt.Sprintf("\t\trelRows, err := q.%s(ctx, relIDs)\n", relName))
				sb.WriteString("\t\tif err != nil {\n\t\t\treturn nil, err\n\t\t}\n")
				relMatchField, ok := FindMatchingField(rq.Ret.Struct.Fields, matchField.Name)
				if !ok {
					relMatchField = rq.Ret.Struct.Fields[0]
				}
				relRetType := rq.Ret.Type()
				if strings.HasPrefix(relRetType, "[]") {
					relRetType = strings.TrimPrefix(relRetType, "[]")
				}
				sb.WriteString(fmt.Sprintf("\t\trelMap := make(map[%s][]%s)\n", sliceType, relRetType))
				sb.WriteString("\t\tfor _, rRow := range relRows {\n")
				if relMatchField.Type == "interface{}" {
					sb.WriteString(fmt.Sprintf("\t\t\tswitch val := rRow.%s.(type) {\n", relMatchField.Name))
					sb.WriteString(fmt.Sprintf("\t\t\tcase %s:\n", sliceType))
					sb.WriteString("\t\t\t\trelMap[val] = append(relMap[val], rRow)\n")
					if sliceType == "int64" {
						sb.WriteString("\t\t\tcase int:\n\t\t\t\trelMap[int64(val)] = append(relMap[int64(val)], rRow)\n")
						sb.WriteString("\t\t\tcase int32:\n\t\t\t\trelMap[int64(val)] = append(relMap[int64(val)], rRow)\n")
					}
					sb.WriteString("\t\t\t}\n")
				} else {
					sb.WriteString(fmt.Sprintf("\t\t\tkey := %s(rRow.%s)\n", sliceType, relMatchField.Name))
					sb.WriteString("\t\t\trelMap[key] = append(relMap[key], rRow)\n")
				}
				sb.WriteString("\t\t}\n")
				sb.WriteString("\t\tfor idx := range items {\n")
				if matchField.Type == "interface{}" {
					sb.WriteString(fmt.Sprintf("\t\t\tswitch val := items[idx].%s.(type) {\n", matchField.Name))
					sb.WriteString(fmt.Sprintf("\t\t\tcase %s:\n", sliceType))
					sb.WriteString(fmt.Sprintf("\t\t\t\titems[idx].%s = relMap[val]\n", f.Name))
					if sliceType == "int64" {
						sb.WriteString("\t\t\tcase int:\n")
						sb.WriteString(fmt.Sprintf("\t\t\t\titems[idx].%s = relMap[int64(val)]\n", f.Name))
						sb.WriteString("\t\t\tcase int32:\n")
						sb.WriteString(fmt.Sprintf("\t\t\t\titems[idx].%s = relMap[int64(val)]\n", f.Name))
					}
					sb.WriteString("\t\t\t}\n")
				} else {
					sb.WriteString(fmt.Sprintf("\t\t\tkey := %s(items[idx].%s)\n", sliceType, matchField.Name))
					sb.WriteString(fmt.Sprintf("\t\t\titems[idx].%s = relMap[key]\n", f.Name))
				}
				sb.WriteString("\t\t}\n")
				sb.WriteString("\t}\n")
			} else {
				sb.WriteString(fmt.Sprintf("\t// Load relation %s for all items\n", f.Name))
				sb.WriteString("\tfor idx := range items {\n")
				if matchField.Type != param.Type {
					sb.WriteString(fmt.Sprintf("\t\tvar paramVal %s\n", param.Type))
					if matchField.Type == "interface{}" {
						sb.WriteString(fmt.Sprintf("\t\tswitch val := items[idx].%s.(type) {\n", matchField.Name))
						sb.WriteString(fmt.Sprintf("\t\tcase %s:\n", param.Type))
						sb.WriteString("\t\t\tparamVal = val\n")
						if param.Type == "int64" {
							sb.WriteString("\t\tcase int:\n\t\t\tparamVal = int64(val)\n")
							sb.WriteString("\t\tcase int32:\n\t\t\tparamVal = int64(val)\n")
						}
						sb.WriteString("\t\t}\n")
					} else {
						sb.WriteString(fmt.Sprintf("\t\tparamVal = %s(items[idx].%s)\n", param.Type, matchField.Name))
					}
					sb.WriteString(fmt.Sprintf("\t\trelVal, err := q.%s(ctx, paramVal)\n", relName))
				} else {
					sb.WriteString(fmt.Sprintf("\t\trelVal, err := q.%s(ctx, items[idx].%s)\n", relName, matchField.Name))
				}
				sb.WriteString("\t\tif err != nil {\n\t\t\treturn nil, err\n\t\t}\n")
				sb.WriteString(fmt.Sprintf("\t\titems[idx].%s = relVal\n", f.Name))
				sb.WriteString("\t}\n")
			}
		}
	}
	return sb.String()
}

func FindMatchingField(fields []Field, paramName string) (Field, bool) {
	for _, f := range fields {
		if strings.EqualFold(f.Name, paramName) {
			return f, true
		}
	}
	for _, f := range fields {
		if strings.EqualFold(f.Name, "ID") || strings.EqualFold(paramName, "ID") {
			return f, true
		}
	}
	if len(fields) > 0 {
		return fields[0], true
	}
	return Field{}, false
}
