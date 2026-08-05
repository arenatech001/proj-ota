package main

import (
	"testing"
)

func TestParseBTDevicesJSON(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
		addr string
	}{
		{
			name: "clean",
			raw:  `[{"address":"D0:06:AD:64:30:6D","name":"GameSir-Nova 2 Lite"}]`,
			want: 1,
			addr: "D0:06:AD:64:30:6D",
		},
		{
			name: "stderr after json",
			raw:  "[{\"address\":\"D0:06:AD:64:30:6D\",\"name\":\"GameSir-Nova 2 Lite\"}]\n2026-08-05T19:41:13+08:00 名称过滤「GameSir」: 匹配 1 台，跳过 15 台",
			want: 1,
			addr: "D0:06:AD:64:30:6D",
		},
		{
			name: "noise before json",
			raw:  "noise\n[]\n",
			want: 0,
		},
		{
			name: "empty",
			raw:  "",
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			devs, err := parseBTDevicesJSON(tc.raw)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if len(devs) != tc.want {
				t.Fatalf("len=%d want=%d", len(devs), tc.want)
			}
			if tc.want > 0 && devs[0].Address != tc.addr {
				t.Fatalf("addr=%q want=%q", devs[0].Address, tc.addr)
			}
		})
	}
}
