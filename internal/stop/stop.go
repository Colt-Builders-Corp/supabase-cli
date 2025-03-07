package stop

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/supabase/cli/internal/utils"
)

var ctx = context.Background()

func Run() error {
	// Sanity checks.
	if err := utils.AssertDockerIsRunning(); err != nil {
		return err
	}
	if err := utils.LoadConfig(); err != nil {
		return err
	}
	if err := utils.AssertSupabaseStartIsRunning(); err != nil {
		fmt.Println(utils.Aqua("supabase") + " local development setup is already stopped.")
		return nil
	}

	// Remove containers.
	{
		containers, err := utils.Docker.ContainerList(ctx, types.ContainerListOptions{
			All:     true,
			Filters: filters.NewArgs(filters.Arg("label", "com.supabase.cli.project="+utils.Config.ProjectId)),
		})
		fmt.Fprintln(os.Stdout, "DockerRun Containers:", containers)
		if err != nil {
			return err
		}

		var wg sync.WaitGroup

		for _, container := range containers {
			wg.Add(1)

			go func(containerId string) {
				_ = utils.Docker.ContainerRemove(ctx, containerId, types.ContainerRemoveOptions{
					RemoveVolumes: true,
					Force:         true,
				})

				wg.Done()
			}(container.ID)
		}

		wg.Wait()
	}

	// Remove networks.
	if _, err := utils.Docker.NetworksPrune(
		ctx,
		filters.NewArgs(filters.Arg("label", "com.supabase.cli.project="+utils.Config.ProjectId)),
	); err != nil {
		return err
	}

	// Remove temporary files.
	if err := os.RemoveAll(".supabase/branches"); err != nil {
		return err
	}

	fmt.Println("Stopped " + utils.Aqua("supabase") + " local development setup.")

	return nil
}
