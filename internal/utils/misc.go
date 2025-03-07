package utils

import (
	"archive/zip"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Update initial schemas in internal/utils/templates/initial_schemas when
// updating any one of these.
const (
	GotrueImage   = "supabase/gotrue:v2.6.18"
	RealtimeImage = "supabase/realtime:v0.22.4"
	StorageImage  = "supabase/storage-api:v0.15.0"
)

const (
	ShadowDbName   = "supabase_shadow"
	KongImage      = "library/kong:2.1"
	InbucketImage  = "inbucket/inbucket:stable"
	PostgrestImage = "postgrest/postgrest:v9.0.0.20220211"
	DifferImage    = "supabase/pgadmin-schema-diff:cli-0.0.4"
	PgmetaImage    = "supabase/postgres-meta:v0.33.2"
	// TODO: Hardcode version once provided upstream.
	StudioImage    = "supabase/studio:latest"
	DenoRelayImage = "supabase/deno-relay:v1.2.0"

	// https://dba.stackexchange.com/a/11895
	// Args: dbname
	TerminateDbSqlFmt = `
SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%[1]s';
-- Wait for WAL sender to drop replication slot.
DO 'BEGIN WHILE (SELECT COUNT(*) FROM pg_replication_slots) > 0 LOOP END LOOP; END';
`
	AnonKey        = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZS1kZW1vIiwicm9sZSI6ImFub24ifQ.625_WdcF3KHqz5amU0x2X5WWHP-OEs_4qj0ssLNHzTs"
	ServiceRoleKey = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZS1kZW1vIiwicm9sZSI6InNlcnZpY2Vfcm9sZSJ9.vI9obAHOGyVVKa3pD--kJlyxp-Z2zV9UUMAhKpNLAcU"
)

//go:embed templates/globals.sql
var GlobalsSql string

func GetCurrentTimestamp() string {
	// Magic number: https://stackoverflow.com/q/45160822.
	return time.Now().UTC().Format("20060102150405")
}

func GetCurrentBranch() (string, error) {
	branch, err := os.ReadFile(".supabase/branches/_current_branch")
	if err != nil {
		return "", err
	}

	return string(branch), nil
}

// TODO: Make all errors use this.
func NewError(s string) error {
	// Ask runtime.Callers for up to 5 PCs, excluding runtime.Callers and NewError.
	pc := make([]uintptr, 5)
	n := runtime.Callers(2, pc)

	pc = pc[:n] // pass only valid pcs to runtime.CallersFrames
	frames := runtime.CallersFrames(pc)

	// Loop to get frames.
	// A fixed number of PCs can expand to an indefinite number of Frames.
	for {
		frame, more := frames.Next()

		// Process this frame.
		//
		// We're only interested in the stack trace in this repo.
		if strings.HasPrefix(frame.Function, "github.com/supabase/cli/internal") {
			s += fmt.Sprintf("\n  in %s:%d", frame.Function, frame.Line)
		}

		// Check whether there are more frames to process after this one.
		if !more {
			break
		}
	}

	return errors.New(s)
}

func AssertSupabaseStartIsRunning() error {
	if err := LoadConfig(); err != nil {
		return err
	}

	if _, err := Docker.ContainerInspect(context.Background(), DbId); err != nil {
		return errors.New(Aqua("supabase start") + " is not running.")
	}

	return nil
}

func GetGitRoot() (*string, error) {
	origWd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	for {
		_, err := os.ReadDir(".git")

		if err == nil {
			gitRoot, err := os.Getwd()
			if err != nil {
				return nil, err
			}

			if err := os.Chdir(origWd); err != nil {
				return nil, err
			}

			return &gitRoot, nil
		}

		if cwd, err := os.Getwd(); err != nil {
			return nil, err
		} else if isRootDirectory(cwd) {
			return nil, nil
		}

		if err := os.Chdir(".."); err != nil {
			return nil, err
		}
	}
}

func IsBranchNameReserved(branch string) bool {
	switch branch {
	case "_current_branch", "main", "supabase_shadow", "postgres", "template0", "template1":
		return true
	default:
		return false
	}
}

func MkdirIfNotExist(path string) error {
	if err := os.Mkdir(path, 0755); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}

	return nil
}


func MkdirAllIfNotExist(path string) error {
	if err := os.MkdirAll(path, 0755); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}

	return nil
}

func AssertSupabaseCliIsSetUp() error {
	if _, err := os.ReadFile("supabase.toml"); errors.Is(err, os.ErrNotExist) {
		return errors.New("Cannot find " + Bold("supabase.toml") + " in the current directory. Have you set up the project with " + Aqua("supabase init") + "?")
	} else if err != nil {
		return err
	}

	return nil
}

func AssertIsLinked() error {
	if _, err := os.Stat(".supabase/temp/project-ref"); errors.Is(err, os.ErrNotExist) {
		return errors.New("Cannot find project ref. Have you run " + Aqua("supabase link") + "?")
	} else if err != nil {
		return err
	}

	return nil
}

func InstallOrUpgradeDeno() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if err := MkdirIfNotExist(filepath.Join(home, ".supabase")); err != nil {
		return err
	}
	denoBinName := "deno"
	if runtime.GOOS == "windows" {
		denoBinName = "deno.exe"
	}
	denoPath := filepath.Join(home, ".supabase", denoBinName)

	if _, err := os.Stat(denoPath); err == nil {
		// Upgrade Deno.

		cmd := exec.Command(denoPath, "upgrade")
		if err := cmd.Run(); err != nil {
			return err
		}

		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	// Install Deno.

	// 1. Determine OS triple
	var assetFilename string
	{
		if runtime.GOOS == "darwin" && runtime.GOARCH == "amd64" {
			assetFilename = "deno-x86_64-apple-darwin.zip"
		} else if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
			assetFilename = "deno-aarch64-apple-darwin.zip"
		} else if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
			assetFilename = "deno-x86_64-unknown-linux-gnu.zip"
		} else if runtime.GOOS == "windows" && runtime.GOARCH == "amd64" {
			assetFilename = "deno-x86_64-pc-windows-msvc.zip"
		} else {
			return errors.New("Platform " + runtime.GOOS + "/" + runtime.GOARCH + " is currently unsupported for Functions.")
		}
	}

	// 2. Download & install Deno binary.
	{
		resp, err := http.Get("https://github.com/denoland/deno/releases/latest/download/" + assetFilename)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			return errors.New("Failed installing Deno binary.")
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		r, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		// There should be only 1 file: the deno binary
		if len(r.File) != 1 {
			return err
		}
		denoContents, err := r.File[0].Open()
		if err != nil {
			return err
		}
		defer denoContents.Close()

		denoBytes, err := io.ReadAll(denoContents)
		if err != nil {
			return err
		}

		if err := os.WriteFile(denoPath, denoBytes, 0755); err != nil {
			return err
		}
	}

	return nil
}

func LoadAccessToken() (string, error) {
	// Env takes precedence
	if accessToken := os.Getenv("SUPABASE_ACCESS_TOKEN"); accessToken != "" {
		matched, err := regexp.MatchString(`^sbp_[a-f0-9]{40}$`, accessToken)
		if err != nil {
			return "", err
		}
		if !matched {
			return "", errors.New("Invalid access token format. Must be like `sbp_0102...1920`.")
		}

		return accessToken, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	accessTokenPath := filepath.Join(home, ".supabase", "access-token")
	accessToken, err := os.ReadFile(accessTokenPath)
	if errors.Is(err, os.ErrNotExist) || string(accessToken) == "" {
		return "", errors.New("Access token not provided. Supply an access token by running " + Aqua("supabase login") + " or setting the SUPABASE_ACCESS_TOKEN environment variable.")
	} else if err != nil {
		return "", err
	}

	return string(accessToken), nil
}

func ValidateFunctionSlug(slug string) error {
	matched, err := regexp.MatchString(`^[A-Za-z][A-Za-z0-9_-]*$`, slug)
	if err != nil {
		return err
	}
	if !matched {
		return errors.New("Invalid Function name. Must start with at least one letter, and only include alphanumeric characters, underscores, and hyphens. (^[A-Za-z][A-Za-z0-9_-]*$)")
	}

	return nil
}

func ShowStatus() {
	fmt.Println(`
         ` + Aqua("API URL") + `: http://localhost:` + strconv.FormatUint(uint64(Config.Api.Port), 10) + `
          ` + Aqua("DB URL") + `: postgresql://postgres:postgres@localhost:` + strconv.FormatUint(uint64(Config.Db.Port), 10) + `/postgres
      ` + Aqua("Studio URL") + `: http://localhost:` + strconv.FormatUint(uint64(Config.Studio.Port), 10) + `
    ` + Aqua("Inbucket URL") + `: http://localhost:` + strconv.FormatUint(uint64(Config.Inbucket.Port), 10) + `
        ` + Aqua("anon key") + `: ` + AnonKey + `
` + Aqua("service_role key") + `: ` + ServiceRoleKey)
}
