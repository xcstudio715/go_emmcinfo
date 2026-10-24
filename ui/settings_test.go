package ui

import (
	"strings"
	"testing"

	"emmc-tool/decoder"
)

// ---------------------------------------------------------------------------
// 无真机 mock：用调试钩子同源的输入驱动 decoder.LifetimeSupport + 弹窗文案分流
// lifetimeBody，断言三种 reason（仅 Android / 仅 eMMC / 两者）对应的正文关键词。
// 不需要真实 adb / GUI。
// ---------------------------------------------------------------------------

func TestLifetimeBodyBranching(t *testing.T) {
	tests := []struct {
		name string
		// fake 环境变量（经 fakeEnvSDK / fakeEnvREV 读入，与生产同一入口）
		sdkEnv string
		revEnv string
		// 无 fake 时生产路径使用的兜底输入（serial 未缓存 SDK 时为 -1）
		fallbackSDK int
		fallbackRev string
		wantSub     []string
		wantNotSub  []string
	}{
		{
			name:        "EMMC_FAKE_SDK=25_仅Android",
			sdkEnv:      "25",
			fallbackRev: "0x8", // eMMC 5.1，不构成不支持项
			wantSub:     []string{"Android"},
			wantNotSub:  []string{"eMMC"},
		},
		{
			name:        "EMMC_FAKE_REV=0x6_仅eMMC",
			revEnv:      "0x6",
			fallbackSDK: 33, // Android 13，不构成不支持项
			wantSub:     []string{"eMMC"},
			wantNotSub:  []string{"Android"},
		},
		{
			name:    "两者都fake_双低",
			sdkEnv:  "25",
			revEnv:  "0x6",
			wantSub: []string{"Android", "eMMC"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.sdkEnv != "" {
				t.Setenv("EMMC_FAKE_SDK", tc.sdkEnv)
			}
			if tc.revEnv != "" {
				t.Setenv("EMMC_FAKE_REV", tc.revEnv)
			}
			// 与 maybeNoticeLifetime 相同的输入合成顺序：fake 优先，否则兜底。
			sdk := tc.fallbackSDK
			if v, ok := fakeEnvSDK(); ok {
				sdk = v
			}
			rev := tc.fallbackRev
			if v, ok := fakeEnvREV(); ok {
				rev = v
			}
			_, reason := decoder.LifetimeSupport(sdk, rev)
			if reason == "" {
				t.Fatalf("期望不支持，got 支持 (sdk=%d rev=%q)", sdk, rev)
			}
			body := lifetimeBody(reason)
			for _, sub := range tc.wantSub {
				if !strings.Contains(body, sub) {
					t.Errorf("正文 = %q, want contains %q", body, sub)
				}
			}
			for _, sub := range tc.wantNotSub {
				if strings.Contains(body, sub) {
					t.Errorf("正文 = %q, want NOT contains %q", body, sub)
				}
			}
		})
	}
}

// 调试钩子健壮性：未设置或解析失败一律视为未设置（返回 ok=false），行为与生产一致。
func TestFakeEnvHooks(t *testing.T) {
	t.Run("未设置", func(t *testing.T) {
		if _, ok := fakeEnvSDK(); ok {
			t.Error("EMMC_FAKE_SDK 未设置应视为未设置")
		}
		if _, ok := fakeEnvREV(); ok {
			t.Error("EMMC_FAKE_REV 未设置应视为未设置")
		}
	})
	t.Run("解析失败", func(t *testing.T) {
		t.Setenv("EMMC_FAKE_SDK", "abc")
		if _, ok := fakeEnvSDK(); ok {
			t.Error(`EMMC_FAKE_SDK="abc" 解析失败应视为未设置`)
		}
		t.Setenv("EMMC_FAKE_REV", "xyz")
		if _, ok := fakeEnvREV(); ok {
			t.Error(`EMMC_FAKE_REV="xyz" 解析失败应视为未设置`)
		}
	})
	t.Run("合法值", func(t *testing.T) {
		t.Setenv("EMMC_FAKE_SDK", "25")
		if v, ok := fakeEnvSDK(); !ok || v != 25 {
			t.Errorf("fakeEnvSDK() = (%d,%v), want (25,true)", v, ok)
		}
		t.Setenv("EMMC_FAKE_REV", "0x6")
		if v, ok := fakeEnvREV(); !ok || v != "0x6" {
			t.Errorf("fakeEnvREV() = (%q,%v), want (\"0x6\",true)", v, ok)
		}
	})
}
