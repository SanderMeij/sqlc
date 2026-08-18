package ast

import "github.com/SanderMeij/sqlc/internal/sql/format"

type ParamRef struct {
	Number   int
	Location int
	Dollar   bool
}

func (n *ParamRef) Pos() int {
	return n.Location
}

func (n *ParamRef) Format(buf *TrackedBuffer, d format.Dialect) {
	if n == nil {
		return
	}
	buf.WriteString(d.Param(n.Number))
}
