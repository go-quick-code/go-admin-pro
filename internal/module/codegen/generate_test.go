package codegen

import "testing"

func TestGenerateReqValidate(t *testing.T) {
	cases := []struct {
		name string
		req  GenerateReq
		ok   bool
	}{
		{"valid", GenerateReq{TableName: "biz_order", ModuleName: "biz", BusinessName: "order", ClassName: "Order"}, true},
		{"bad table", GenerateReq{TableName: "../../etc/passwd", ModuleName: "biz", BusinessName: "order"}, false},
		{"bad module", GenerateReq{TableName: "biz_order", ModuleName: "Biz-Module", BusinessName: "order"}, false},
		{"bad business", GenerateReq{TableName: "biz_order", ModuleName: "biz", BusinessName: "order/list"}, false},
		{"bad class", GenerateReq{TableName: "biz_order", ModuleName: "biz", BusinessName: "order", ClassName: "1Order"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.req.validate()
			if tc.ok && err != nil {
				t.Fatalf("expected valid, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestGoTypeMapping(t *testing.T) {
	cases := map[string]string{
		"bigint":   "int64",
		"int":      "int32",
		"tinyint":  "int8",
		"decimal":  "float64",
		"datetime": "*time.Time",
		"varchar":  "string",
	}
	for dataType, want := range cases {
		if got := goType(ColumnInfo{DataType: dataType}); got != want {
			t.Fatalf("goType(%q) = %q, want %q", dataType, got, want)
		}
	}
}
