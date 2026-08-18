package rewrite

import (
	"fmt"
	"strings"

	"github.com/SanderMeij/sqlc/internal/source"
	"github.com/SanderMeij/sqlc/internal/sql/ast"
	"github.com/SanderMeij/sqlc/internal/sql/astutils"
)

// Relation is an instance of `sqlc.relation(query)`
type Relation struct {
	Name string
	Node *ast.A_Const
}

// Orig string to replace
func (r Relation) Orig() string {
	return fmt.Sprintf("sqlc.relation(%s)", r.Name)
}

// RelationSet is a set of Relation instances
type RelationSet []*Relation

// Find a matching relation by node
func (rs RelationSet) Find(node *ast.A_Const) (*Relation, bool) {
	for _, r := range rs {
		if r.Node == node {
			return r, true
		}
	}
	return nil, false
}

func relationFromFuncCall(call *ast.FuncCall) (string, string) {
	queryName, isConst := flatten(call.Args)
	origName := queryName
	if isConst {
		origName = fmt.Sprintf("'%s'", queryName)
	}

	funcName := call.Func.Schema + "." + call.Func.Name
	spaces := ""
	if call.Args != nil && len(call.Args.Items) > 0 {
		leftParen := call.Args.Items[0].Pos() - 1
		spaces = strings.Repeat(" ", leftParen-call.Location-len(funcName))
	}
	origText := fmt.Sprintf("%s%s(%s)", funcName, spaces, origName)
	return queryName, origText
}

// Relations rewrites `sqlc.relation(query)` to a `ast.A_Const` of value `NULL`.
// The compiler can make use of the returned `RelationSet` and edits.
func Relations(raw *ast.RawStmt) (*ast.RawStmt, RelationSet, []source.Edit) {
	var relations []*Relation
	var edits []source.Edit

	node := astutils.Apply(raw, func(cr *astutils.Cursor) bool {
		node := cr.Node()

		switch {
		case isRelation(node):
			fun := node.(*ast.FuncCall)

			if len(fun.Args.Items) == 0 {
				return false
			}

			queryName, origText := relationFromFuncCall(fun)
			if queryName == "" {
				return false
			}

			node := &ast.A_Const{
				Val: &ast.Null{},
			}

			relations = append(relations, &Relation{
				Name: queryName,
				Node: node,
			})

			edits = append(edits, source.Edit{
				Location: fun.Location - raw.StmtLocation,
				Old:      origText,
				New:      "NULL",
			})

			cr.Replace(node)
			return false
		default:
			return true
		}
	}, nil)

	return node.(*ast.RawStmt), relations, edits
}

func isRelation(node ast.Node) bool {
	call, ok := node.(*ast.FuncCall)
	if !ok {
		return false
	}

	if call.Func == nil {
		return false
	}

	isValid := call.Func.Schema == "sqlc" && call.Func.Name == "relation"
	return isValid
}
