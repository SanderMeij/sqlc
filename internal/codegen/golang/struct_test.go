package golang

import (
	"testing"
)

func TestStructHasIDField(t *testing.T) {
	tests := []struct {
		name     string
		fields   []Field
		wantHas  bool
		wantType string
	}{
		{
			name: "has ID field",
			fields: []Field{
				{Name: "ID", Type: "int64"},
				{Name: "Name", Type: "string"},
			},
			wantHas:  true,
			wantType: "int64",
		},
		{
			name: "has lowercase id field",
			fields: []Field{
				{Name: "id", Type: "int64"},
				{Name: "Name", Type: "string"},
			},
			wantHas:  false,
			wantType: "",
		},
		{
			name: "no ID field",
			fields: []Field{
				{Name: "Name", Type: "string"},
			},
			wantHas:  false,
			wantType: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := Struct{
				Name:   "User",
				Fields: tc.fields,
			}
			if got := s.HasIDField(); got != tc.wantHas {
				t.Errorf("HasIDField() = %v, want %v", got, tc.wantHas)
			}
			if got := s.IDFieldType(); got != tc.wantType {
				t.Errorf("IDFieldType() = %q, want %q", got, tc.wantType)
			}
		})
	}
}
