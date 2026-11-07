package model

import (
	"reflect"
	"testing"
)

func TestInt64ListScanAndValue(t *testing.T) {
	var list Int64List
	if err := list.Scan([]byte(`[1,2,3]`)); err != nil {
		t.Fatalf("Scan []byte: %v", err)
	}
	if !reflect.DeepEqual([]int64(list), []int64{1, 2, 3}) {
		t.Fatalf("Scan 结果不符：%+v", list)
	}

	if err := list.Scan("[4,5]"); err != nil {
		t.Fatalf("Scan string: %v", err)
	}
	if !reflect.DeepEqual([]int64(list), []int64{4, 5}) {
		t.Fatalf("Scan string 结果不符：%+v", list)
	}

	if err := list.Scan(nil); err != nil {
		t.Fatalf("Scan nil: %v", err)
	}
	if list != nil {
		t.Fatalf("Scan nil 应清空，实际 %+v", list)
	}

	value, err := Int64List{7, 8}.Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	if value != "[7,8]" {
		t.Fatalf("Value 结果不符：%v", value)
	}

	empty, err := Int64List(nil).Value()
	if err != nil {
		t.Fatalf("nil Value: %v", err)
	}
	if empty != "[]" {
		t.Fatalf("nil Value 应输出 []，实际 %v", empty)
	}
}
