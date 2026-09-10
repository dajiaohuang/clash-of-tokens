package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"clash-of-tokens/catalog"
	"clash-of-tokens/internal/api"
	"clash-of-tokens/internal/chatgptweb"
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
	"clash-of-tokens/internal/providers/appdevice"
	"clash-of-tokens/internal/secrets"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "clash-tokens:", e)
		os.Exit(1)
	}
}
func run() error {
	command := "serve"
	args := os.Args[1:]
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	path := fs.String("config", "config.json", "configuration file")
	provider := fs.String("provider", "", "provider preset")
	model := fs.String("model", "", "upstream model id")
	baseURL := fs.String("base-url", "", "provider endpoint override; required for account-specific tenants")
	project := fs.String("project", "", "provider project or website post id (see provider documentation)")
	enable := fs.Bool("enable", false, "enable the configured source (Auto remains unapproved)")
	if e := fs.Parse(args); e != nil {
		return e
	}
	if command == "providers" {
		fmt.Println(string(catalog.Data))
		return nil
	}
	if command == "init" {
		c := config.Default()
		if *provider != "" {
			p, e := catalog.Preset(*provider, *model, *baseURL)
			if e != nil {
				return e
			}
			p.Enabled = *enable
			p.Project = *project
			c.Sources = append(c.Sources, p)
			if p.Adapter == "chatgpt-web" || p.Adapter == "cloudflare-playground" {
				c.Browser.Enabled = true
			}
		}
		if e := c.Validate(); e != nil {
			return e
		}
		f, e := os.OpenFile(*path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		defer f.Close()
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		return enc.Encode(c)
	}
	if command == "version" {
		fmt.Println("clash-tokens 0.1.0-dev")
		return nil
	}
	if command != "serve" && command != "validate" && command != "browser-login" && command != "doctor" && command != "device-doctor" && command != "keys" {
		return fmt.Errorf("usage: clash-tokens {init|providers|validate|serve|browser-login|doctor|device-doctor|keys|version} [-config path]")
	}
	c, e := config.Load(*path)
	if e != nil {
		return e
	}
	if command == "validate" {
		fmt.Printf("configuration valid: %d sources, %d groups\n", len(c.Sources), len(c.Groups))
		return nil
	}
	if command == "device-doctor" {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		return json.NewEncoder(os.Stdout).Encode(appdevice.Check(ctx, c.Device))
	}
	if command == "browser-login" {
		return browserLogin(c)
	}
	if command == "doctor" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		data, e := chatgptweb.New(c.Browser, "chatgpt-web").Check(ctx)
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(data)
	}
	keys := secrets.Keys{API: os.Getenv(c.APIKeyEnv), Admin: os.Getenv(c.AdminKeyEnv)}
	if keys.API == "" || keys.Admin == "" {
		saved, e := secrets.LoadOrCreate(filepath.Join(filepath.Dir(c.Browser.StateFile), "gateway-keys"))
		if e != nil {
			return e
		}
		if keys.API == "" {
			keys.API = saved.API
		}
		if keys.Admin == "" {
			keys.Admin = saved.Admin
		}
	}
	if command == "keys" {
		return json.NewEncoder(os.Stdout).Encode(keys)
	}
	vault, e := credentials.Open(filepath.Join(filepath.Dir(c.Browser.StateFile), "credentials.vault"))
	if e != nil {
		return e
	}
	handler, e := api.NewControlPlane(*path, c, keys.API, keys.Admin, vault)
	if e != nil {
		return e
	}
	defer handler.Close()
	server := &http.Server{Addr: c.Listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			deadline, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if server.Shutdown(deadline) != nil {
				_ = server.Close()
			}
		case <-done:
		}
	}()
	defer close(done)
	listener, e := net.Listen("tcp", c.Listen)
	if e != nil {
		return e
	}
	fmt.Println("Clash of Tokens listening on", c.Listen)
	e = server.Serve(api.LimitListener(listener, c.Runtime.MaxInflight+c.Runtime.MaxQueued+32))
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}
