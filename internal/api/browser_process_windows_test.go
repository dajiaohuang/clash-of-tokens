//go:build windows

package api

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestOwnedWindowsBrowserTreeLifecycle(t *testing.T) {
	for _, action := range []string{"stop", "gateway_close", "root_exit"} {
		t.Run(action, func(t *testing.T) {
			dir := t.TempDir()
			pidFile, exitFile := filepath.Join(dir, "child.pid"), filepath.Join(dir, "exit")
			unrelated := exec.Command(os.Args[0], "-test.run=^TestBrowserTreeHelper$", "--", "leaf")
			if err := unrelated.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = unrelated.Process.Kill(); _ = unrelated.Wait() }()
			other, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(unrelated.Process.Pid))
			if err != nil {
				t.Fatal(err)
			}
			defer windows.CloseHandle(other)
			b := &browserProcesses{}
			defer b.close()
			view, err := b.start("fixture", exec.Command(os.Args[0], "-test.run=^TestBrowserTreeHelper$", "--", "root", pidFile, exitFile))
			if err != nil {
				t.Fatal(err)
			}
			var pid int
			deadline := time.Now().Add(5 * time.Second)
			for pid == 0 {
				raw, _ := os.ReadFile(pidFile)
				pid, _ = strconv.Atoi(string(raw))
				if time.Now().After(deadline) {
					t.Fatal("owned root did not create child")
				}
				time.Sleep(10 * time.Millisecond)
			}
			child, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
			if err != nil {
				t.Fatal(err)
			}
			defer windows.CloseHandle(child)
			switch action {
			case "stop":
				err = b.stop(view.ID)
			case "gateway_close":
				b.close()
			case "root_exit":
				err = os.WriteFile(exitFile, []byte("exit"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			state, err := windows.WaitForSingleObject(child, 3000)
			if err != nil || state != windows.WAIT_OBJECT_0 {
				t.Fatal("owned descendant survived", state, err)
			}
			state, err = windows.WaitForSingleObject(other, 0)
			if err != nil || state != uint32(windows.WAIT_TIMEOUT) {
				t.Fatal("unrelated process affected", state, err)
			}
		})
	}
}

func TestBrowserTreeHelper(t *testing.T) {
	index := -1
	for i, arg := range os.Args {
		if arg == "--" {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	mode := os.Args[index+1]
	if mode == "root" {
		child := exec.Command(os.Args[0], "-test.run=^TestBrowserTreeHelper$", "--", "leaf")
		if child.Start() != nil {
			os.Exit(3)
		}
		if os.WriteFile(os.Args[index+2], []byte(fmt.Sprint(child.Process.Pid)), 0600) != nil {
			os.Exit(4)
		}
		for {
			if _, err := os.Stat(os.Args[index+3]); err == nil {
				os.Exit(0)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	for {
		time.Sleep(time.Minute)
	}
}
