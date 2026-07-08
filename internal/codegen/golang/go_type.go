package golang

import (
	"fmt"
	"maps"
	"strings"

	"github.com/sqlc-dev/sqlc/internal/codegen/golang/opts"
	"github.com/sqlc-dev/sqlc/internal/codegen/sdk"
	"github.com/sqlc-dev/sqlc/internal/inflection"
	"github.com/sqlc-dev/sqlc/internal/plugin"
)

func addExtraGoStructTags(tags map[string]string, req *plugin.GenerateRequest, options *opts.Options, col *plugin.Column) {
	for _, override := range options.Overrides {
		oride := override.ShimOverride
		if oride.GoType.StructTags == nil {
			continue
		}
		if override.MatchesColumn(col) {
			maps.Copy(tags, oride.GoType.StructTags)
			continue
		}
		if !override.Matches(col.Table, req.Catalog.DefaultSchema) {
			// Different table.
			continue
		}
		cname := col.Name
		if col.OriginalName != "" {
			cname = col.OriginalName
		}
		if !sdk.MatchString(oride.ColumnName, cname) {
			// Different column.
			continue
		}
		// Add the extra tags.
		maps.Copy(tags, oride.GoType.StructTags)
	}
}

func goType(req *plugin.GenerateRequest, options *opts.Options, col *plugin.Column) string {
	if strings.HasPrefix(col.Type.Name, "relation:") {
		return goRelationType(req, options, col)
	}

	// Check if the column's type has been overridden
	for _, override := range options.Overrides {
		oride := override.ShimOverride

		if oride.GoType.TypeName == "" {
			continue
		}
		cname := col.Name
		if col.OriginalName != "" {
			cname = col.OriginalName
		}
		sameTable := override.Matches(col.Table, req.Catalog.DefaultSchema)
		if oride.Column != "" && sdk.MatchString(oride.ColumnName, cname) && sameTable {
			if col.IsSqlcSlice {
				return "[]" + oride.GoType.TypeName
			}
			return oride.GoType.TypeName
		}
	}
	typ := goInnerType(req, options, col)
	if col.IsSqlcSlice {
		return "[]" + typ
	}
	if col.IsArray {
		return strings.Repeat("[]", int(col.ArrayDims)) + typ
	}
	return typ
}

func goInnerType(req *plugin.GenerateRequest, options *opts.Options, col *plugin.Column) string {
	// package overrides have a higher precedence
	for _, override := range options.Overrides {
		oride := override.ShimOverride
		if oride.GoType.TypeName == "" {
			continue
		}
		if override.MatchesColumn(col) {
			return oride.GoType.TypeName
		}
	}

	// TODO: Extend the engine interface to handle types
	switch req.Settings.Engine {
	case "mysql":
		return mysqlType(req, options, col)
	case "postgresql":
		return postgresType(req, options, col)
	case "sqlite":
		return sqliteType(req, options, col)
	default:
		return "interface{}"
	}
}

func goRelationType(req *plugin.GenerateRequest, options *opts.Options, col *plugin.Column) string {
	relationQueryName := strings.TrimPrefix(col.Type.Name, "relation:")
	var relQuery *plugin.Query
	for _, q := range req.Queries {
		if q.Name == relationQueryName {
			relQuery = q
			break
		}
	}
	if relQuery == nil {
		return "interface{}"
	}

	typ := relationType(req, options, relQuery)
	if relQuery.Cmd == ":many" {
		var idCol *plugin.Column
		for _, c := range relQuery.Columns {
			if strings.ToLower(c.Name) == "id" {
				idCol = c
				break
			}
		}
		keyType := "interface{}"
		if idCol != nil {
			keyType = goType(req, options, idCol)
		}
		return fmt.Sprintf("map[%s]%s", keyType, typ)
	}
	return typ
}

func relationType(req *plugin.GenerateRequest, options *opts.Options, relQuery *plugin.Query) string {
	if len(relQuery.Columns) == 0 {
		return "interface{}"
	}

	// Find tables to compare
	var tables []*plugin.Table
	for _, schema := range req.Catalog.Schemas {
		tables = append(tables, schema.Tables...)
	}

	for _, table := range tables {
		if len(table.Columns) == len(relQuery.Columns) {
			same := true
			for i, c := range relQuery.Columns {
				tc := table.Columns[i]
				if strings.ToLower(c.Name) != strings.ToLower(tc.Name) {
					same = false
					break
				}
			}
			if same {
				structName := table.Rel.Name
				if !options.EmitExactTableNames {
					structName = inflection.Singular(inflection.SingularParams{
						Name:       structName,
						Exclusions: options.InflectionExcludeTableNames,
					})
				}
				return StructName(structName, options)
			}
		}
	}

	return relQuery.Name + "Row"
}

