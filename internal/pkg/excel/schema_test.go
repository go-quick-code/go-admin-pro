package excel_test

import (
	"errors"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/excel"
)

type sampleRow struct {
	Name     string    `excel:"客户名称,required,width=24"`
	Age      int       `excel:"年龄"`
	Amount   float64   `excel:"金额,width=16"`
	Active   bool      `excel:"启用"`
	JoinedAt time.Time `excel:"加入日期"`
	Note     string    `excel:"备注"`
	Ignored  string    `excel:"-"`
	Internal string
}

func TestColumns(t *testing.T) {
	cols, err := excel.Columns[sampleRow]()
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}
	if len(cols) != 6 {
		t.Fatalf("got %d columns, want 6: %+v", len(cols), cols)
	}

	wantHeaders := []string{"客户名称", "年龄", "金额", "启用", "加入日期", "备注"}
	for i, want := range wantHeaders {
		if cols[i].Header != want {
			t.Errorf("column %d header = %q, want %q", i, cols[i].Header, want)
		}
	}
	if cols[0].Field != "Name" || !cols[0].Required || cols[0].Width != 24 {
		t.Errorf("unexpected first column: %+v", cols[0])
	}
	if cols[2].Width != 16 {
		t.Errorf("width = %v, want 16", cols[2].Width)
	}
	for _, c := range cols {
		if c.Field == "Ignored" || c.Field == "Internal" {
			t.Errorf("field %q should not be mapped", c.Field)
		}
	}
}

type embeddedBase struct {
	Owner string `excel:"负责人"`
}

type embeddedRow struct {
	embeddedBase
	Name string `excel:"名称"`
}

func TestColumnsEmbedded(t *testing.T) {
	cols, err := excel.Columns[embeddedRow]()
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}
	if len(cols) != 2 || cols[0].Field != "Owner" || cols[1].Field != "Name" {
		t.Fatalf("unexpected embedded columns: %+v", cols)
	}
}

type unsupportedFieldRow struct {
	Name  string            `excel:"名称"`
	Attrs map[string]string `excel:"属性"`
}

type conflictingHeaderRow struct {
	Code string `excel:"Name"`
	Name string `excel:"名称"`
}

type duplicateFieldBaseA struct {
	Code string `excel:"编码A"`
}

type duplicateFieldBaseB struct {
	Code string `excel:"编码B"`
}

type duplicateFieldRow struct {
	duplicateFieldBaseA
	duplicateFieldBaseB
}

func TestColumnsErrors(t *testing.T) {
	cases := []struct {
		name    string
		columns func() ([]excel.Column, error)
		wantIs  error
	}{
		{"empty header", func() ([]excel.Column, error) {
			type row struct {
				Name string `excel:",required"`
			}
			return excel.Columns[row]()
		}, excel.ErrInvalidSchema},
		{"unknown option", func() ([]excel.Column, error) {
			type row struct {
				Name string `excel:"名称,unicorn"`
			}
			return excel.Columns[row]()
		}, excel.ErrInvalidSchema},
		{"bad width", func() ([]excel.Column, error) {
			type row struct {
				Name string `excel:"名称,width=abc"`
			}
			return excel.Columns[row]()
		}, excel.ErrInvalidSchema},
		{"nan width", func() ([]excel.Column, error) {
			type row struct {
				Name string `excel:"名称,width=NaN"`
			}
			return excel.Columns[row]()
		}, excel.ErrInvalidSchema},
		{"width too large", func() ([]excel.Column, error) {
			type row struct {
				Name string `excel:"名称,width=256"`
			}
			return excel.Columns[row]()
		}, excel.ErrInvalidSchema},
		{"duplicate header", func() ([]excel.Column, error) {
			type row struct {
				Name  string `excel:"名称"`
				Title string `excel:"名称"`
			}
			return excel.Columns[row]()
		}, excel.ErrInvalidSchema},
		{"no mapped columns", func() ([]excel.Column, error) {
			type row struct {
				Name string
			}
			return excel.Columns[row]()
		}, excel.ErrInvalidSchema},
		{"unsupported field type", func() ([]excel.Column, error) {
			return excel.Columns[unsupportedFieldRow]()
		}, excel.ErrUnsupportedType},
		{"header conflicts with field name", func() ([]excel.Column, error) {
			return excel.Columns[conflictingHeaderRow]()
		}, excel.ErrInvalidSchema},
		{"duplicate embedded field", func() ([]excel.Column, error) {
			return excel.Columns[duplicateFieldRow]()
		}, excel.ErrInvalidSchema},
		{"non struct", func() ([]excel.Column, error) {
			return excel.Columns[string]()
		}, excel.ErrUnsupportedType},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.columns()
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, tc.wantIs) {
				t.Fatalf("error = %v, want errors.Is %v", err, tc.wantIs)
			}
		})
	}
}
