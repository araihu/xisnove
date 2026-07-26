package command

import (
	"net/http"
	"strconv"

	"github.com/araihu/xisnove/cli/internal/output"
	"github.com/spf13/cobra"
)

func newStatusCommand(runtime Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show public aggregate status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, _, err := runtime.OpenClient(false)
			if err != nil {
				return localFailure("open profile", err)
			}
			response, err := client.GetPublicStatusPageWithResponse(cmd.Context())
			if err != nil {
				return remoteFailure("get public status", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			status := response.JSON200
			return renderRemote(runtime, status, output.Table{
				Headers: []string{"STATE", "MONITORS", "ACTIVE INCIDENTS", "GENERATED AT"},
				Rows: [][]string{{
					string(status.State),
					strconv.Itoa(len(status.Monitors)),
					strconv.Itoa(len(status.ActiveIncidents)),
					timeValue(status.GeneratedAt),
				}},
			})
		},
	}
}
