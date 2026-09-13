package credentials

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// ExternalReference stores only an explicitly selected official-manager field.
// It never enumerates a vault, reads secure notes or exports a password store.
type ExternalReference struct {
	Manager   string `json:"manager"`
	Reference string `json:"reference"`
	Field     string `json:"field,omitempty"`
}

var managerItemID = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

func (ref ExternalReference) Validate() error {
	if len(ref.Reference) > 1024 || strings.ContainsAny(ref.Reference, "\r\n\x00") {
		return errors.New("invalid external reference")
	}
	switch ref.Manager {
	case "1password":
		parts := strings.Split(strings.TrimPrefix(ref.Reference, "op://"), "/")
		if !strings.HasPrefix(ref.Reference, "op://") || (len(parts) != 3 && len(parts) != 4) || ref.Field != "" {
			break
		}
		for _, part := range parts {
			if part == "" || strings.ContainsAny(part, "?#") {
				return errors.New("invalid selected 1Password field")
			}
		}
		return nil
	case "bitwarden":
		if managerItemID.MatchString(ref.Reference) && (ref.Field == "password" || ref.Field == "username") {
			return nil
		}
	}
	return errors.New("select an op:// vault/item/field or a Bitwarden item UUID and password/username field")
}

type boundedOutput struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (b *boundedOutput) Len() int       { return b.buffer.Len() }
func (b *boundedOutput) String() string { return b.buffer.String() }

func (b *boundedOutput) Write(data []byte) (int, error) {
	if b.Len()+len(data) > b.limit {
		b.exceeded = true
		return 0, errors.New("manager output limit")
	}
	return b.buffer.Write(data)
}
func ReadExternal(ctx context.Context, ref ExternalReference) (string, error) {
	return readExternal(ctx, ref, exec.CommandContext)
}
func readExternal(ctx context.Context, ref ExternalReference, commandFor func(context.Context, string, ...string) *exec.Cmd) (string, error) {
	if err := ref.Validate(); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	binary, args := "op", []string{"read", ref.Reference, "--no-newline"}
	if ref.Manager == "bitwarden" {
		binary = "bw"
		args = []string{"get", ref.Field, ref.Reference, "--raw"}
	}
	command := commandFor(ctx, binary, args...)
	// Only official CLI session variables and normal OS paths are inherited.
	allowed := map[string]bool{"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "WINDIR": true, "HOME": true, "USERPROFILE": true, "APPDATA": true, "LOCALAPPDATA": true, "TEMP": true, "TMP": true, "XDG_CONFIG_HOME": true, "XDG_RUNTIME_DIR": true, "DBUS_SESSION_BUS_ADDRESS": true}
	if ref.Manager == "bitwarden" {
		allowed["BW_SESSION"] = true
		allowed["BITWARDENCLI_APPDATA_DIR"] = true
	} else {
		allowed["OP_SERVICE_ACCOUNT_TOKEN"] = true
		allowed["OP_ACCOUNT"] = true
		allowed["OP_CONFIG_DIR"] = true
	}
	for _, item := range os.Environ() {
		key, _, ok := strings.Cut(item, "=")
		if ok && allowed[strings.ToUpper(key)] {
			command.Env = append(command.Env, item)
		}
	}
	command.Stdin = nil
	output := &boundedOutput{limit: 1 << 20}
	command.Stdout = output
	// Discard upstream diagnostics: they may contain selected values or account data.
	command.Stderr = nil
	command.WaitDelay = time.Second
	if err := command.Run(); err != nil || output.exceeded {
		return "", errors.New("manager unavailable, locked, authorization denied or selected field missing")
	}
	value := strings.TrimSuffix(strings.TrimSuffix(output.String(), "\n"), "\r")
	if value == "" {
		return "", errors.New("selected manager field is empty")
	}
	return value, nil
}
func (s *Store) PutExternal(id, kind string, ref ExternalReference) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if kind != "api_key" && kind != "oauth" && kind != "cookie" {
		return errors.New("external references require an invocation credential type")
	}
	// Stage metadata with no upstream access. Saving a reference never prompts
	// for manager authorization; only an explicit check/invocation resolves it.
	return s.putRecord(id, kind, "external:"+ref.Manager, "", &ref, nil)
}
