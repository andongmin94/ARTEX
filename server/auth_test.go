package server

import (
	"strings"
	"testing"
)

func TestValidatePassword(t *testing.T) {
	for _, tc := range []struct {
		name, pw string
		wantErr  bool
	}{
		{"빈 비밀번호", "", true},
		{"ASCII 7자", "1234567", true},
		{"ASCII 8자", "12345678", false},
		{"한글 3자 9바이트", "가나다", true},
		{"한글 8자", strings.Repeat("가", 8), false},
		{"이모지 7자", strings.Repeat("😀", 7), true},
		{"이모지 8자", strings.Repeat("😀", 8), false},
		{"ASCII 72바이트", strings.Repeat("a", 72), false},
		{"ASCII 73바이트", strings.Repeat("a", 73), true},
		{"한글 72바이트", strings.Repeat("가", 24), false},
		{"한글 75바이트", strings.Repeat("가", 25), true},
		{"이모지 72바이트", strings.Repeat("😀", 18), false},
		{"이모지 76바이트", strings.Repeat("😀", 19), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validatePassword(tc.pw); (got != "") != tc.wantErr {
				t.Fatalf("validatePassword(%q)=%q, wantErr=%v", tc.pw, got, tc.wantErr)
			}
		})
	}
}
