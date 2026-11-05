package service

import (
	"strings"
	"testing"

	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// isNotFound 断言错误是 404 业务错误（看不见的即不存在）。
func isNotFound(err error) bool {
	e, ok := xerr.As(err)
	return ok && e.Status == 404
}

// 合同附件纯函数测试（issue #17）：上传准入只看合同状态，作废合同（终态、
// 签约凭据失效）拒绝上传；草稿/待审/已签都允许补充签约资料。
func TestValidateContractFileUpload(t *testing.T) {
	legal := []int8{
		model.ContractStatusDraft,
		model.ContractStatusPending,
		model.ContractStatusSigned,
	}
	for _, status := range legal {
		c := &model.Contract{Status: status}
		if err := ValidateContractFileUpload(c); err != nil {
			t.Fatalf("状态 %d 应允许上传附件，实际 %v", status, err)
		}
	}

	c := &model.Contract{Status: model.ContractStatusVoid}
	err := ValidateContractFileUpload(c)
	if !isBadRequest(err) {
		t.Fatalf("作废合同上传应 400，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "作废") {
		t.Fatalf("作废合同上传文案应点明「作废」，实际 %q", err.Error())
	}

	if err := ValidateContractFileUpload(nil); !isNotFound(err) {
		t.Fatalf("合同不存在应 404，实际 %v", err)
	}
}
