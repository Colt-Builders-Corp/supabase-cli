package serve

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"path/filepath"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/joho/godotenv"
	"github.com/supabase/cli/internal/utils"
)

var ctx = context.Background()

func Run(slug string, envFilePath string, verifyJWT bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	if err := utils.LoadConfig(); err != nil {
		return err
	}

	edgeFuncSrcPath := filepath.Join(cwd, utils.Config.Edgefunctions.SrcPath)
	edgeFuncSlugPath := filepath.Join("/home/deno", utils.Config.Edgefunctions.FunctionsPath, slug, "index.ts")

	// 1. Sanity checks.
	{
		if err := utils.AssertSupabaseCliIsSetUp(); err != nil {
			return err
		}
		if err := utils.AssertDockerIsRunning(); err != nil {
			return err
		}
		if err := utils.AssertSupabaseStartIsRunning(); err != nil {
			return err
		}
		if err := utils.ValidateFunctionSlug(slug); err != nil {
			return err
		}
		if envFilePath != "" {
			if _, err := os.ReadFile(envFilePath); err != nil {
				return fmt.Errorf("Failed to read env file: %w", err)
			}
		}
	}

	// 2. Stop on SIGINT/SIGTERM.
	{
		termCh := make(chan os.Signal, 1)
		signal.Notify(termCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-termCh
			_ = utils.Docker.ContainerRemove(ctx, utils.DenoRelayId, types.ContainerRemoveOptions{
				RemoveVolumes: true,
				Force:         true,
			})
		}()
	}

	// 3. Start relay.
	{
		_ = utils.Docker.ContainerRemove(ctx, utils.DenoRelayId, types.ContainerRemoveOptions{
			RemoveVolumes: true,
			Force:         true,
		})

		env := []string{
			"JWT_SECRET=super-secret-jwt-token-with-at-least-32-characters-long",
			"DENO_ORIGIN=http://localhost:8000",
		}
		if verifyJWT {
			env = append(env, "VERIFY_JWT=true")
		} else {
			env = append(env, "VERIFY_JWT=false")
		}

		if _, err := utils.DockerRun(
			ctx,
			utils.DenoRelayId,
			&container.Config{
				Image: utils.DenoRelayImage,
				Env:   env,
				Labels: map[string]string{
					"com.supabase.cli.project":   utils.Config.ProjectId,
					"com.docker.compose.project": utils.Config.ProjectId,
				},
			},
			&container.HostConfig{
				Binds:       []string{edgeFuncSrcPath+":/home/deno:ro,z"},
				NetworkMode: container.NetworkMode(utils.NetId),
			},
		); err != nil {
			return err
		}
	}

	// 4. Start Function.
	{
		fmt.Println("Starting " + utils.Bold(edgeFuncSlugPath))
		out, err := utils.DockerExec(ctx, utils.DenoRelayId, []string{
			"deno", "cache", edgeFuncSlugPath,
		})
		if err != nil {
			return err
		}
		if _, err := stdcopy.StdCopy(io.Discard, io.Discard, out); err != nil {
			return err
		}
	}

	{
		fmt.Println("Serving " + utils.Bold(edgeFuncSlugPath))

		env := []string{
			"SUPABASE_URL=http://" + utils.KongId + ":8000",
			"SUPABASE_ANON_KEY=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZS1kZW1vIiwicm9sZSI6ImFub24ifQ.625_WdcF3KHqz5amU0x2X5WWHP-OEs_4qj0ssLNHzTs",
			"SUPABASE_SERVICE_ROLE_KEY=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZS1kZW1vIiwicm9sZSI6InNlcnZpY2Vfcm9sZSJ9.vI9obAHOGyVVKa3pD--kJlyxp-Z2zV9UUMAhKpNLAcU",
			"SUPABASE_DB_URL=postgresql://postgres:postgres@localhost:" + strconv.FormatUint(uint64(utils.Config.Db.Port), 10) + "/postgres",
		}

		if envFilePath == "" {
			// skip
		} else {
			envMap, err := godotenv.Read(envFilePath)
			if err != nil {
				return err
			}
			for name, value := range envMap {
				if strings.HasPrefix(name, "SUPABASE_") {
					return errors.New("Invalid secret name: " + name + ". Secret names cannot start with SUPABASE_.")
				}
				env = append(env, name+"="+value)
			}
		}

		exec, err := utils.Docker.ContainerExecCreate(
			ctx,
			utils.DenoRelayId,
			types.ExecConfig{
				Env: env,
				Cmd: []string{
					"deno", "run", "--no-check=remote", "--allow-all", "--watch", "--no-clear-screen", edgeFuncSlugPath,
				},
				AttachStderr: true,
				AttachStdout: true,
			},
		)
		if err != nil {
			return err
		}

		resp, err := utils.Docker.ContainerExecAttach(ctx, exec.ID, types.ExecStartCheck{})
		if err != nil {
			return err
		}

		if err := utils.Docker.ContainerExecStart(ctx, exec.ID, types.ExecStartCheck{}); err != nil {
			return err
		}

		if _, err := stdcopy.StdCopy(os.Stdout, os.Stderr, resp.Reader); err != nil {
			return err
		}
	}

	fmt.Println("Stopped serving " + utils.Bold(edgeFuncSlugPath))
	return nil
}
