package credentials

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOfficialManagerCommandBoundary(t *testing.T) {
	for _, manager := range []string{"1password", "bitwarden"} {
		t.Run(manager, func(t *testing.T) {
			t.Setenv("COT_UNRELATED_SECRET", "must-not-inherit")
			t.Setenv("OP_ACCOUNT", "selected-account")
			t.Setenv("BW_SESSION", "selected-session")
			ref := ExternalReference{Manager: manager, Reference: "op://vault/item/password"}
			binary, args := "op", []string{"read", ref.Reference, "--no-newline"}
			if manager == "bitwarden" {
				ref.Reference = "01234567-0123-0123-0123-0123456789ab"
				ref.Field = "password"
				binary = "bw"
				args = []string{"get", "password", ref.Reference, "--raw"}
			}
			value, err := readExternal(context.Background(), ref, func(ctx context.Context, name string, actual ...string) *exec.Cmd {
				if name != binary || !reflect.DeepEqual(actual, args) {
					t.Fatal("manager command broadened", name, actual)
				}
				return exec.CommandContext(ctx, os.Args[0], "-test.run=^TestManagerHelperProcess$", "--", manager, "normal")
			})
			if err != nil || value != "synthetic-selected-secret" {
				t.Fatal("selected field unavailable", err)
			}
		})
	}
	for _, mode := range []string{"oversize", "cancel", "failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			value, err := readExternal(ctx, ExternalReference{Manager: "1password", Reference: "op://vault/item/password"}, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
				return exec.CommandContext(ctx, os.Args[0], "-test.run=^TestManagerHelperProcess$", "--", "1password", mode)
			})
			if err == nil || value != "" || strings.Contains(err.Error(), "canary") {
				t.Fatal("unsafe manager failure", err)
			}
		})
	}
}

func TestManagerHelperProcess(t *testing.T) {
	index := -1
	for i, v := range os.Args {
		if v == "--" {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	manager, mode := os.Args[index+1], os.Args[index+2]
	if os.Getenv("COT_UNRELATED_SECRET") != "" || (manager == "bitwarden" && os.Getenv("OP_ACCOUNT") != "") || (manager == "1password" && os.Getenv("BW_SESSION") != "") {
		os.Exit(2)
	}
	switch mode {
	case "cancel":
		time.Sleep(10 * time.Second)
	case "oversize":
		fmt.Print(strings.Repeat("x", (1<<20)+1))
	case "failure":
		fmt.Fprint(os.Stderr, "canary-upstream-secret")
		os.Exit(3)
	default:
		fmt.Print("synthetic-selected-secret")
	}
	os.Exit(0)
}
