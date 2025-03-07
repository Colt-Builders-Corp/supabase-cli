package start

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
	"github.com/muesli/reflow/wrap"
	"github.com/supabase/cli/internal/utils"
)

// TODO: Handle cleanup on SIGINT/SIGTERM.
func Run() error {
	// Sanity checks.
	{
		if err := utils.AssertSupabaseCliIsSetUp(); err != nil {
			return err
		}
		if err := utils.AssertDockerIsRunning(); err != nil {
			return err
		}
		if err := utils.LoadConfig(); err != nil {
			return err
		}
		if err := utils.InterpolateEnvInConfig(); err != nil {
			return err
		}
		if err := utils.AssertSupabaseStartIsRunning(); err == nil {
			return errors.New(utils.Aqua("supabase start") + " is already running. Try running " + utils.Aqua("supabase stop") + " first.")
		}
	}

	s := spinner.NewModel()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	p := utils.NewProgram(model{spinner: s})

	errCh := make(chan error, 1)
	go func() {
		errCh <- run(p)
		p.Send(tea.Quit())
	}()

	if err := p.Start(); err != nil {
		return err
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return errors.New("Aborted " + utils.Aqua("supabase start") + ".")
	}
	if err := <-errCh; err != nil {
		utils.DockerRemoveAll()
		_ = utils.Docker.NetworkRemove(context.Background(), utils.NetId)
		return err
	}

	fmt.Println("Started " + utils.Aqua("supabase") + " local development setup.")
	utils.ShowStatus()

	return nil
}

var (
	ctx, cancelCtx = context.WithCancel(context.Background())

	// TODO: Unhardcode keys
	//go:embed templates/kong_config
	kongConfigEmbed       string
	kongConfigTemplate, _ = template.New("kongConfig").Parse(kongConfigEmbed)
)

func run(p utils.Program) error {
	_, _ = utils.Docker.NetworkCreate(
		ctx,
		utils.NetId,
		types.NetworkCreate{
			CheckDuplicate: true,
			Labels: map[string]string{
				"com.supabase.cli.project":   utils.Config.ProjectId,
				"com.docker.compose.project": utils.Config.ProjectId,
			},
		},
	)

	// Ensure `_current_branch` file exists.
	if _, err := os.ReadFile(".supabase/branches/_current_branch"); err == nil {
		// skip
	} else if errors.Is(err, os.ErrNotExist) {
		if err := utils.MkdirIfNotExist(".supabase/branches"); err != nil {
			return err
		}
		if err := os.WriteFile(".supabase/branches/_current_branch", []byte("main"), 0644); err != nil {
			return err
		}
	} else {
		return err
	}
	currBranch, err := utils.GetCurrentBranch()
	if err != nil {
		return err
	}

	p.Send(utils.StatusMsg("Pulling images..."))

	// Pull images.
	{
		p.Send(utils.StatusMsg("docker.io/"+utils.DbImage))

		if _, _, err := utils.Docker.ImageInspectWithRaw(ctx, "docker.io/"+utils.DbImage); err != nil {
			out, err := utils.Docker.ImagePull(
				ctx,
				"docker.io/"+utils.DbImage,
				types.ImagePullOptions{},
			)
			if err != nil {
				return err
			}
			if err := utils.ProcessPullOutput(out, p); err != nil {
				return err
			}
		}
		p.Send(utils.StatusMsg("docker.io/"+utils.KongImage))
		if _, _, err := utils.Docker.ImageInspectWithRaw(ctx, "docker.io/"+utils.KongImage); err != nil {
			out, err := utils.Docker.ImagePull(
				ctx,
				"docker.io/"+utils.KongImage,
				types.ImagePullOptions{},
			)
			if err != nil {
				return err
			}
			if err := utils.ProcessPullOutput(out, p); err != nil {
				return err
			}
		}
		p.Send(utils.StatusMsg("docker.io/"+utils.GotrueImage))
		if _, _, err := utils.Docker.ImageInspectWithRaw(ctx, "docker.io/"+utils.GotrueImage); err != nil {
			out, err := utils.Docker.ImagePull(
				ctx,
				"docker.io/"+utils.GotrueImage,
				types.ImagePullOptions{},
			)
			if err != nil {
				return err
			}
			if err := utils.ProcessPullOutput(out, p); err != nil {
				return err
			}
		}
		p.Send(utils.StatusMsg("docker.io/"+utils.InbucketImage))
		if _, _, err := utils.Docker.ImageInspectWithRaw(ctx, "docker.io/"+utils.InbucketImage); err != nil {
			out, err := utils.Docker.ImagePull(
				ctx,
				"docker.io/"+utils.InbucketImage,
				types.ImagePullOptions{},
			)
			if err != nil {
				return err
			}
			if err := utils.ProcessPullOutput(out, p); err != nil {
				return err
			}
		}
		p.Send(utils.StatusMsg("docker.io/"+utils.RealtimeImage))
		if _, _, err := utils.Docker.ImageInspectWithRaw(ctx, "docker.io/"+utils.RealtimeImage); err != nil {
			out, err := utils.Docker.ImagePull(
				ctx,
				"docker.io/"+utils.RealtimeImage,
				types.ImagePullOptions{},
			)
			if err != nil {
				return err
			}
			if err := utils.ProcessPullOutput(out, p); err != nil {
				return err
			}
		}
		p.Send(utils.StatusMsg("docker.io/"+utils.PostgrestImage))
		if _, _, err := utils.Docker.ImageInspectWithRaw(ctx, "docker.io/"+utils.PostgrestImage); err != nil {
			out, err := utils.Docker.ImagePull(
				ctx,
				"docker.io/"+utils.PostgrestImage,
				types.ImagePullOptions{},
			)
			if err != nil {
				return err
			}
			if err := utils.ProcessPullOutput(out, p); err != nil {
				return err
			}
		}
		p.Send(utils.StatusMsg("docker.io/"+utils.StorageImage))
		if _, _, err := utils.Docker.ImageInspectWithRaw(ctx, "docker.io/"+utils.StorageImage); err != nil {
			out, err := utils.Docker.ImagePull(
				ctx,
				"docker.io/"+utils.StorageImage,
				types.ImagePullOptions{},
			)
			if err != nil {
				return err
			}
			if err := utils.ProcessPullOutput(out, p); err != nil {
				return err
			}
		}
		p.Send(utils.StatusMsg("docker.io/"+utils.DifferImage))
		if _, _, err := utils.Docker.ImageInspectWithRaw(ctx, "docker.io/"+utils.DifferImage); err != nil {
			out, err := utils.Docker.ImagePull(
				ctx,
				"docker.io/"+utils.DifferImage,
				types.ImagePullOptions{},
			)
			if err != nil {
				return err
			}
			if err := utils.ProcessPullOutput(out, p); err != nil {
				return err
			}
		}
		p.Send(utils.StatusMsg("docker.io/"+utils.PgmetaImage))
		if _, _, err := utils.Docker.ImageInspectWithRaw(ctx, "docker.io/"+utils.PgmetaImage); err != nil {
			out, err := utils.Docker.ImagePull(
				ctx,
				"docker.io/"+utils.PgmetaImage,
				types.ImagePullOptions{},
			)
			if err != nil {
				return err
			}
			if err := utils.ProcessPullOutput(out, p); err != nil {
				return err
			}
		}
		p.Send(utils.StatusMsg("docker.io/"+utils.StudioImage))
		if _, _, err := utils.Docker.ImageInspectWithRaw(ctx, "docker.io/"+utils.StudioImage); err != nil {
			out, err := utils.Docker.ImagePull(
				ctx,
				"docker.io/"+utils.StudioImage,
				types.ImagePullOptions{},
			)
			if err != nil {
				return err
			}
			if err := utils.ProcessPullOutput(out, p); err != nil {
				return err
			}
		}
		p.Send(utils.StatusMsg("docker.io/"+utils.DenoRelayImage))
		if _, _, err := utils.Docker.ImageInspectWithRaw(ctx, "docker.io/"+utils.DenoRelayImage); err != nil {
			out, err := utils.Docker.ImagePull(
				ctx,
				"docker.io/"+utils.DenoRelayImage,
				types.ImagePullOptions{},
			)
			if err != nil {
				return err
			}
			if err := utils.ProcessPullOutput(out, p); err != nil {
				return err
			}
		}
	}

	p.Send(utils.StatusMsg("Starting database..."))

	// Start Postgres.
	{
		cmd := []string{}
		if utils.Config.Db.MajorVersion >= 14 {
			cmd = []string{"postgres", "-c", "config_file=/etc/postgresql/postgresql.conf"}
		}

		if _, err := utils.DockerRun(
			ctx,
			utils.DbId,
			&container.Config{
				Image: utils.DbImage,
				Env:   []string{"POSTGRES_PASSWORD=postgres"},
				Cmd:   cmd,
				Labels: map[string]string{
					"com.supabase.cli.project":   utils.Config.ProjectId,
					"com.docker.compose.project": utils.Config.ProjectId,
				},
			},
			&container.HostConfig{
				NetworkMode:   container.NetworkMode(utils.NetId),
				PortBindings:  nat.PortMap{"5432/tcp": []nat.PortBinding{{HostPort: strconv.FormatUint(uint64(utils.Config.Db.Port), 10)}}},
				RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
			},
		); err != nil {
			p.Send(utils.StatusMsg(err.Error()))
			return err
		}

		out, err := utils.DockerExec(ctx, utils.DbId, []string{
			"sh", "-c", "until pg_isready --host $(hostname --ip-address); do sleep 0.1; done " +
				`&& psql --username postgres --host localhost <<'EOSQL'
BEGIN;
` + utils.GlobalsSql + `
COMMIT;
EOSQL
`,
		})
		if err != nil {
			return err
		}
		var errBuf bytes.Buffer
		if _, err := stdcopy.StdCopy(io.Discard, &errBuf, out); err != nil {
			return err
		}
		if errBuf.Len() > 0 {
			return errors.New("Error starting database: " + errBuf.String())
		}
	}

	p.Send(utils.StatusMsg("Restoring branches..."))

	// Restore branches.
	{
		if branches, err := os.ReadDir(".supabase/branches"); err == nil {
			for _, branch := range branches {
				if branch.Name() == "_current_branch" {
					continue
				}

				if err := func() error {
					content, err := os.ReadFile(".supabase/branches/" + branch.Name() + "/dump.sql")
					if errors.Is(err, os.ErrNotExist) {
						return errors.New("Branch was not dumped.")
					} else if err != nil {
						return err
					}

					out, err := utils.DockerExec(ctx, utils.DbId, []string{
						"sh", "-c", `psql --set ON_ERROR_STOP=on postgresql://postgres:postgres@localhost/postgres <<'EOSQL'
CREATE DATABASE "` + branch.Name() + `";
\connect ` + branch.Name() + `
BEGIN;
` + string(content) + `
COMMIT;
EOSQL
`,
					})
					if err != nil {
						return err
					}
					if err := utils.ProcessPsqlOutput(out, p); err != nil {
						return fmt.Errorf("Error starting database: %w", err)
					}

					return nil
				}(); err != nil {
					_ = os.RemoveAll(".supabase/branches/" + branch.Name())
					_ = os.WriteFile(".supabase/branches/_current_branch", []byte("main"), 0644)
					fmt.Fprintln(os.Stderr, "Error restoring branch "+utils.Aqua(branch.Name())+":", err)
				}
			}
		} else if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(".supabase/branches", 0755); err != nil {
				return err
			}
		} else {
			return err
		}

		// Ensure `main` branch exists.
		if _, err := os.ReadDir(".supabase/branches/main"); err == nil {
			// skip
		} else if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(".supabase/branches/main", 0755); err != nil {
				return err
			}

			if err := func() error {
				{
					out, err := utils.DockerExec(ctx, utils.DbId, []string{
						"createdb", "--username", "postgres", "--host", "localhost", "main",
					})
					if err != nil {
						return err
					}
					var errBuf bytes.Buffer
					if _, err := stdcopy.StdCopy(io.Discard, &errBuf, out); err != nil {
						return err
					}
					if errBuf.Len() > 0 {
						return errors.New("Error creating database: " + errBuf.String())
					}
				}

				p.Send(utils.StatusMsg("Setting up initial schema..."))
				{
					out, err := utils.DockerExec(ctx, utils.DbId, []string{
						"sh", "-c", `PGOPTIONS='--client-min-messages=error' psql postgresql://postgres:postgres@localhost/main <<'EOSQL'
BEGIN;
` + utils.InitialSchemaSql + `
COMMIT;
EOSQL
`,
					})
					if err != nil {
						return err
					}
					var errBuf bytes.Buffer
					if _, err := stdcopy.StdCopy(io.Discard, &errBuf, out); err != nil {
						return err
					}
					if errBuf.Len() > 0 {
						return errors.New("Error starting database: " + errBuf.String())
					}
				}

				p.Send(utils.StatusMsg("Applying " + utils.Bold(".supabase/extensions.sql") + "..."))
				{
					extensionsSql, err := os.ReadFile(".supabase/extensions.sql")
					if errors.Is(err, os.ErrNotExist) {
						// skip
					} else if err != nil {
						return err
					} else {
						out, err := utils.DockerExec(ctx, utils.DbId, []string{
							"psql", "postgresql://postgres:postgres@localhost/main", "-c", string(extensionsSql),
						})
						if err != nil {
							return err
						}
						var errBuf bytes.Buffer
						if _, err := stdcopy.StdCopy(io.Discard, &errBuf, out); err != nil {
							return err
						}
						if errBuf.Len() > 0 {
							return errors.New("Error starting database: " + errBuf.String())
						}
					}
				}

				if err := utils.MkdirIfNotExist(".supabase/migrations"); err != nil {
					return err
				}
				migrations, err := os.ReadDir(".supabase/migrations")
				if err != nil {
					return err
				}

				for i, migration := range migrations {
					// NOTE: To handle backward-compatibility.
					// `<timestamp>_init.sql` as the first migration (prev
					// versions of the CLI) is deprecated.
					if i == 0 {
						matches := regexp.MustCompile(`([0-9]{14})_init\.sql`).FindStringSubmatch(migration.Name())
						if len(matches) == 2 {
							if timestamp, err := strconv.ParseUint(matches[1], 10, 64); err != nil {
								return err
							} else if timestamp < 20211209000000 {
								continue
							}
						}
					}

					p.Send(utils.StatusMsg("Applying migration " + utils.Bold(migration.Name()) + "..."))

					content, err := os.ReadFile(".supabase/migrations/" + migration.Name())
					if err != nil {
						return err
					}

					out, err := utils.DockerExec(ctx, utils.DbId, []string{
						"sh", "-c", `PGOPTIONS='--client-min-messages=error' psql postgresql://postgres:postgres@localhost/main <<'EOSQL'
BEGIN;
` + string(content) + `
COMMIT;
EOSQL
`,
					})
					if err != nil {
						return err
					}
					var errBuf bytes.Buffer
					if _, err := stdcopy.StdCopy(io.Discard, &errBuf, out); err != nil {
						return err
					}
					if errBuf.Len() > 0 {
						return errors.New("Error starting database: " + errBuf.String())
					}
				}

				p.Send(utils.StatusMsg("Applying " + utils.Bold(".supabase/seed.sql") + "..."))
				{
					content, err := os.ReadFile(".supabase/seed.sql")
					if errors.Is(err, os.ErrNotExist) {
						// skip
					} else if err != nil {
						return err
					} else {
						out, err := utils.DockerExec(ctx, utils.DbId, []string{
							"psql", "postgresql://postgres:postgres@localhost/main", "-c", string(content),
						})
						if err != nil {
							return err
						}
						var errBuf bytes.Buffer
						if _, err := stdcopy.StdCopy(io.Discard, &errBuf, out); err != nil {
							return err
						}
						if errBuf.Len() > 0 {
							return errors.New("Error starting database: " + errBuf.String())
						}
					}
				}

				return nil
			}(); err != nil {
				_ = os.RemoveAll(".supabase/branches/main")
				return err
			}
		} else {
			return err
		}

		// Set up current branch.
		{
			out, err := utils.DockerExec(ctx, utils.DbId, []string{
				"sh", "-c", `psql --set ON_ERROR_STOP=on postgresql://postgres:postgres@localhost/template1 <<'EOSQL'
BEGIN;
` + fmt.Sprintf(utils.TerminateDbSqlFmt, "postgres") + `
COMMIT;
DROP DATABASE postgres;
ALTER DATABASE "` + currBranch + `" RENAME TO postgres;
EOSQL
`,
			})
			if err != nil {
				return err
			}
			var errBuf bytes.Buffer
			if _, err := stdcopy.StdCopy(io.Discard, &errBuf, out); err != nil {
				return err
			}
			if errBuf.Len() > 0 {
				return errors.New("Error starting database: " + errBuf.String())
			}
		}
	}

	p.Send(utils.StatusMsg("Starting containers..."))

	// Start Kong.
	{
		var kongConfigBuf bytes.Buffer
		if err := kongConfigTemplate.Execute(&kongConfigBuf, struct{ ProjectId string }{ProjectId: utils.Config.ProjectId}); err != nil {
			return err
		}

		if _, err := utils.DockerRun(
			ctx,
			utils.KongId,
			&container.Config{
				Image: utils.KongImage,
				Env: []string{
					"KONG_DATABASE=off",
					"KONG_DECLARATIVE_CONFIG=/home/kong/kong.yml",
					"KONG_DNS_ORDER=LAST,A,CNAME", // https://github.com/supabase/cli/issues/14
					"KONG_PLUGINS=request-transformer,cors,key-auth",
				},
				Entrypoint: []string{"sh", "-c", `cat <<'EOF' > /home/kong/kong.yml && ./docker-entrypoint.sh kong docker-start
` + kongConfigBuf.String() + `
EOF
`},
				Labels: map[string]string{
					"com.supabase.cli.project":   utils.Config.ProjectId,
					"com.docker.compose.project": utils.Config.ProjectId,
				},
			},
			&container.HostConfig{
				NetworkMode:   container.NetworkMode(utils.NetId),
				PortBindings:  nat.PortMap{"8000/tcp": []nat.PortBinding{{HostPort: strconv.FormatUint(uint64(utils.Config.Api.Port), 10)}}},
				RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
			},
		); err != nil {
			return err
		}
	}

	// Start GoTrue.
	{
		env := []string{
			fmt.Sprintf("API_EXTERNAL_URL=http://localhost:%v", utils.Config.Api.Port),

			"GOTRUE_API_HOST=0.0.0.0",
			"GOTRUE_API_PORT=9999",

			"GOTRUE_DB_DRIVER=postgres",
			"GOTRUE_DB_DATABASE_URL=postgresql://supabase_auth_admin:postgres@" + utils.DbId + ":5432/postgres",

			"GOTRUE_SITE_URL=" + utils.Config.Auth.SiteUrl,
			"GOTRUE_URI_ALLOW_LIST=" + strings.Join(utils.Config.Auth.AdditionalRedirectUrls, ","),
			fmt.Sprintf("GOTRUE_DISABLE_SIGNUP=%v", !*utils.Config.Auth.EnableSignup),

			"GOTRUE_JWT_ADMIN_ROLES=service_role",
			"GOTRUE_JWT_AUD=authenticated",
			"GOTRUE_JWT_DEFAULT_GROUP_NAME=authenticated",
			fmt.Sprintf("GOTRUE_JWT_EXP=%v", utils.Config.Auth.JwtExpiry),
			"GOTRUE_JWT_SECRET=super-secret-jwt-token-with-at-least-32-characters-long",

			fmt.Sprintf("GOTRUE_EXTERNAL_EMAIL_ENABLED=%v", *utils.Config.Auth.Email.EnableSignup),
			fmt.Sprintf("GOTRUE_MAILER_SECURE_EMAIL_CHANGE_ENABLED=%v", *utils.Config.Auth.Email.DoubleConfirmChanges),
			fmt.Sprintf("GOTRUE_MAILER_AUTOCONFIRM=%v", !*utils.Config.Auth.Email.EnableConfirmations),

			"GOTRUE_SMTP_HOST=" + utils.InbucketId,
			"GOTRUE_SMTP_PORT=2500",
			"GOTRUE_SMTP_USER=GOTRUE_SMTP_USER",
			"GOTRUE_SMTP_PASS=GOTRUE_SMTP_PASS",
			"GOTRUE_SMTP_ADMIN_EMAIL=admin@email.com",
			"GOTRUE_SMTP_MAX_FREQUENCY=1s",
			"GOTRUE_MAILER_URLPATHS_INVITE=/auth/v1/verify",
			"GOTRUE_MAILER_URLPATHS_CONFIRMATION=/auth/v1/verify",
			"GOTRUE_MAILER_URLPATHS_RECOVERY=/auth/v1/verify",
			"GOTRUE_MAILER_URLPATHS_EMAIL_CHANGE=/auth/v1/verify",

			"GOTRUE_EXTERNAL_PHONE_ENABLED=true",
			"GOTRUE_SMS_AUTOCONFIRM=true",
		}

		for name, config := range utils.Config.Auth.External {
			env = append(
				env,
				fmt.Sprintf("GOTRUE_EXTERNAL_%s_ENABLED=%v", strings.ToUpper(name), config.Enabled),
				fmt.Sprintf("GOTRUE_EXTERNAL_%s_CLIENT_ID=%s", strings.ToUpper(name), config.ClientId),
				fmt.Sprintf("GOTRUE_EXTERNAL_%s_SECRET=%s", strings.ToUpper(name), config.Secret),
				fmt.Sprintf("GOTRUE_EXTERNAL_%s_REDIRECT_URI=http://localhost:%v/auth/v1/callback", strings.ToUpper(name), utils.Config.Api.Port),
			)
		}

		if _, err := utils.DockerRun(
			ctx,
			utils.GotrueId,
			&container.Config{
				Image: utils.GotrueImage,
				Env:   env,
				Labels: map[string]string{
					"com.supabase.cli.project":   utils.Config.ProjectId,
					"com.docker.compose.project": utils.Config.ProjectId,
				},
			},
			&container.HostConfig{
				NetworkMode:   container.NetworkMode(utils.NetId),
				RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
			},
		); err != nil {
			return err
		}
	}

	// Start Inbucket.
	if _, err := utils.DockerRun(
		ctx,
		utils.InbucketId,
		&container.Config{
			Image: utils.InbucketImage,
			Labels: map[string]string{
				"com.supabase.cli.project":   utils.Config.ProjectId,
				"com.docker.compose.project": utils.Config.ProjectId,
			},
		},
		&container.HostConfig{
			NetworkMode:   container.NetworkMode(utils.NetId),
			PortBindings:  nat.PortMap{"9000/tcp": []nat.PortBinding{{HostPort: strconv.FormatUint(uint64(utils.Config.Inbucket.Port), 10)}}},
			RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		},
	); err != nil {
		return err
	}

	// Start Realtime.
	if _, err := utils.DockerRun(
		ctx,
		utils.RealtimeId,
		&container.Config{
			Image: utils.RealtimeImage,
			Env: []string{
				"PORT=4000",
				"DB_HOST=" + utils.DbId,
				"DB_PORT=5432",
				"DB_USER=postgres",
				"DB_PASSWORD=postgres",
				"DB_NAME=postgres",
				"DB_SSL=false",
				"SLOT_NAME=supabase_realtime",
				"TEMPORARY_SLOT=true",
				"JWT_SECRET=super-secret-jwt-token-with-at-least-32-characters-long",
				"SECURE_CHANNELS=true",
				"REPLICATION_MODE=RLS",
				"REPLICATION_POLL_INTERVAL=100",
			},
			Labels: map[string]string{
				"com.supabase.cli.project":   utils.Config.ProjectId,
				"com.docker.compose.project": utils.Config.ProjectId,
			},
		},
		&container.HostConfig{
			NetworkMode:   container.NetworkMode(utils.NetId),
			RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		}); err != nil {
		return err
	}

	// Start PostgREST.
	if _, err := utils.DockerRun(
		ctx,
		utils.RestId,
		&container.Config{
			Image: utils.PostgrestImage,
			Env: []string{
				"PGRST_DB_URI=postgresql://postgres:postgres@" + utils.DbId + ":5432/postgres",
				"PGRST_DB_SCHEMAS=" + strings.Join(append([]string{"public", "storage", "graphql_public"}, utils.Config.Api.Schemas...), ","),
				"PGRST_DB_EXTRA_SEARCH_PATH=" + strings.Join(append([]string{"public"}, utils.Config.Api.ExtraSearchPath...), ","),
				"PGRST_DB_ANON_ROLE=anon",
				"PGRST_JWT_SECRET=super-secret-jwt-token-with-at-least-32-characters-long",
			},
			Labels: map[string]string{
				"com.supabase.cli.project":   utils.Config.ProjectId,
				"com.docker.compose.project": utils.Config.ProjectId,
			},
		},
		&container.HostConfig{
			NetworkMode:   container.NetworkMode(utils.NetId),
			RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		},
	); err != nil {
		return err
	}

	// Start Storage.
	if _, err := utils.DockerRun(
		ctx,
		utils.StorageId,
		&container.Config{
			Image: utils.StorageImage,
			Env: []string{
				"ANON_KEY=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZS1kZW1vIiwicm9sZSI6ImFub24ifQ.625_WdcF3KHqz5amU0x2X5WWHP-OEs_4qj0ssLNHzTs",
				"SERVICE_KEY=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZS1kZW1vIiwicm9sZSI6InNlcnZpY2Vfcm9sZSJ9.vI9obAHOGyVVKa3pD--kJlyxp-Z2zV9UUMAhKpNLAcU",
				"POSTGREST_URL=http://" + utils.RestId + ":3000",
				"PGRST_JWT_SECRET=super-secret-jwt-token-with-at-least-32-characters-long",
				"DATABASE_URL=postgresql://supabase_storage_admin:postgres@" + utils.DbId + ":5432/postgres",
				"FILE_SIZE_LIMIT=52428800",
				"STORAGE_BACKEND=file",
				"FILE_STORAGE_BACKEND_PATH=/var/lib/storage",
				"TENANT_ID=stub",
				// TODO: https://github.com/supabase/storage-api/issues/55
				"REGION=stub",
				"GLOBAL_S3_BUCKET=stub",
			},
			Labels: map[string]string{
				"com.supabase.cli.project":   utils.Config.ProjectId,
				"com.docker.compose.project": utils.Config.ProjectId,
			},
		},
		&container.HostConfig{
			NetworkMode:   container.NetworkMode(utils.NetId),
			RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		},
	); err != nil {
		return err
	}

	// Start diff tool.
	if _, err := utils.DockerRun(
		ctx,
		utils.DifferId,
		&container.Config{
			Image:      utils.DifferImage,
			Entrypoint: []string{"sleep", "infinity"},
			Labels: map[string]string{
				"com.supabase.cli.project":   utils.Config.ProjectId,
				"com.docker.compose.project": utils.Config.ProjectId,
			},
		},
		&container.HostConfig{
			NetworkMode:   container.NetworkMode(utils.NetId),
			RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		},
	); err != nil {
		return err
	}

	// Start pg-meta.
	if _, err := utils.DockerRun(
		ctx,
		utils.PgmetaId,
		&container.Config{
			Image: utils.PgmetaImage,
			Env: []string{
				"PG_META_PORT=8080",
				"PG_META_DB_HOST=" + utils.DbId,
			},
			Labels: map[string]string{
				"com.supabase.cli.project":   utils.Config.ProjectId,
				"com.docker.compose.project": utils.Config.ProjectId,
			},
		},
		&container.HostConfig{
			NetworkMode:   container.NetworkMode(utils.NetId),
			RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		},
	); err != nil {
		return err
	}

	// Start Studio.
	if _, err := utils.DockerRun(
		ctx,
		utils.StudioId,
		&container.Config{
			Image: utils.StudioImage,
			Env: []string{
				"STUDIO_PG_META_URL=http://" + utils.PgmetaId + ":8080",
				"POSTGRES_PASSWORD=postgres",

				"SUPABASE_URL=http://" + utils.KongId + ":8000",
				fmt.Sprintf("SUPABASE_REST_URL=http://localhost:%v/rest/v1/", utils.Config.Api.Port),
				"SUPABASE_ANON_KEY=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZS1kZW1vIiwicm9sZSI6ImFub24ifQ.625_WdcF3KHqz5amU0x2X5WWHP-OEs_4qj0ssLNHzTs",
				"SUPABASE_SERVICE_KEY=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZS1kZW1vIiwicm9sZSI6InNlcnZpY2Vfcm9sZSJ9.vI9obAHOGyVVKa3pD--kJlyxp-Z2zV9UUMAhKpNLAcU",
			},
			Labels: map[string]string{
				"com.supabase.cli.project":   utils.Config.ProjectId,
				"com.docker.compose.project": utils.Config.ProjectId,
			},
		},
		&container.HostConfig{
			NetworkMode:   container.NetworkMode(utils.NetId),
			PortBindings:  nat.PortMap{"3000/tcp": []nat.PortBinding{{HostPort: strconv.FormatUint(uint64(utils.Config.Studio.Port), 10)}}},
			RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		},
	); err != nil {
		return err
	}

	return nil
}

type model struct {
	spinner     spinner.Model
	status      string
	progress    *progress.Model
	psqlOutputs []string

	width int
}

func (m model) Init() tea.Cmd {
	return spinner.Tick
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			// Stop future runs
			cancelCtx()
			// Stop current runs
			utils.DockerRemoveAll()
			return m, tea.Quit
		default:
			return m, nil
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case spinner.TickMsg:
		spinnerModel, cmd := m.spinner.Update(msg)
		m.spinner = spinnerModel
		return m, cmd
	case progress.FrameMsg:
		if m.progress == nil {
			return m, nil
		}

		tmp, cmd := m.progress.Update(msg)
		progressModel := tmp.(progress.Model)
		m.progress = &progressModel
		return m, cmd
	case utils.StatusMsg:
		m.status = string(msg)
		return m, nil
	case utils.ProgressMsg:
		if msg == nil {
			m.progress = nil
			return m, nil
		}

		if m.progress == nil {
			progressModel := progress.NewModel(progress.WithGradient("#1c1c1c", "#34b27b"))
			m.progress = &progressModel
		}

		return m, m.progress.SetPercent(*msg)
	case utils.PsqlMsg:
		if msg == nil {
			m.psqlOutputs = []string{}
			return m, nil
		}

		m.psqlOutputs = append(m.psqlOutputs, *msg)
		if len(m.psqlOutputs) > 5 {
			m.psqlOutputs = m.psqlOutputs[1:]
		}
		return m, nil
	default:
		return m, nil
	}
}

func (m model) View() string {
	var progress string
	if m.progress != nil {
		progress = "\n\n" + m.progress.View()
	}

	var psqlOutputs string
	if len(m.psqlOutputs) > 0 {
		psqlOutputs = "\n\n" + strings.Join(m.psqlOutputs, "\n")
	}

	return wrap.String(m.spinner.View()+m.status+progress+psqlOutputs, m.width)
}
