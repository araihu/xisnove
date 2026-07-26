package command

import (
	"net/http"

	"github.com/araihu/xisnove/cli/internal/input"
	"github.com/araihu/xisnove/cli/internal/output"
	"github.com/araihu/xisnove/cli/internal/problem"
	"github.com/araihu/xisnove/sdk"
	"github.com/spf13/cobra"
)

func newMaintenanceCommand(runtime Runtime) *cobra.Command {
	command := &cobra.Command{Use: "maintenance", Short: "Manage maintenance intervals"}
	command.AddCommand(newMaintenanceListCommand(runtime), newMaintenanceGetCommand(runtime), newMaintenanceCreateCommand(runtime), newMaintenanceEndCommand(runtime), newMaintenanceDeleteCommand(runtime))
	return command
}

func newMaintenanceGetCommand(runtime Runtime) *cobra.Command {
	return &cobra.Command{
		Use: "get ID", Short: "Get a maintenance interval", Args: exactArgs(1, "maintenance get requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("maintenance ID", args[0])
			if err != nil {
				return err
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.GetMaintenanceWithResponse(cmd.Context(), id)
			if err != nil {
				return remoteFailure("get maintenance", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			return renderMaintenance(runtime, response.JSON200)
		},
	}
}

func newMaintenanceCreateCommand(runtime Runtime) *cobra.Command {
	var file, key string
	command := &cobra.Command{
		Use: "create", Short: "Create maintenance from a generated-SDK request", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if file == "" {
				return problem.Usage("--file is required")
			}
			var body sdk.CreateMaintenanceJSONRequestBody
			if err := input.DecodeFile(file, runtime.Stdin, &body); err != nil {
				return problem.Usage(err.Error())
			}
			resolved, editors, err := mutationEditors(runtime, key)
			if err != nil {
				return err
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.CreateMaintenanceWithResponse(cmd.Context(), &sdk.CreateMaintenanceParams{IdempotencyKey: &resolved}, body, editors...)
			if err != nil {
				return remoteFailure("create maintenance", err)
			}
			if response.StatusCode() != http.StatusCreated || response.JSON201 == nil {
				return responseProblem(response)
			}
			return renderMaintenance(runtime, response.JSON201)
		},
	}
	addFileMutationFlags(command, &file, &key)
	return command
}

func newMaintenanceEndCommand(runtime Runtime) *cobra.Command {
	var key string
	command := &cobra.Command{
		Use: "end ID", Short: "End active maintenance now", Args: exactArgs(1, "maintenance end requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("maintenance ID", args[0])
			if err != nil {
				return err
			}
			resolved, editors, err := mutationEditors(runtime, key)
			if err != nil {
				return err
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.EndMaintenanceWithResponse(cmd.Context(), id, &sdk.EndMaintenanceParams{IdempotencyKey: &resolved}, editors...)
			if err != nil {
				return remoteFailure("end maintenance", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			return renderMaintenance(runtime, response.JSON200)
		},
	}
	addMutationFlag(command, &key)
	return command
}

func newMaintenanceDeleteCommand(runtime Runtime) *cobra.Command {
	var key string
	command := &cobra.Command{
		Use: "delete ID", Short: "Delete future maintenance", Args: exactArgs(1, "maintenance delete requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("maintenance ID", args[0])
			if err != nil {
				return err
			}
			_, editors, err := mutationEditors(runtime, key)
			if err != nil {
				return err
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.DeleteMaintenanceWithResponse(cmd.Context(), id, editors...)
			if err != nil {
				return remoteFailure("delete maintenance", err)
			}
			if response.StatusCode() != http.StatusNoContent {
				return responseProblem(response)
			}
			return renderAction(runtime, "maintenance", id.String(), "deleted")
		},
	}
	addMutationFlag(command, &key)
	return command
}

func renderMaintenance(runtime Runtime, item *sdk.Maintenance) error {
	return renderRemote(runtime, item, output.Table{Headers: []string{"ID", "MONITOR ID", "REASON", "STARTS AT", "ENDS AT"}, Rows: [][]string{{item.Id.String(), item.MonitorId.String(), item.Reason, timeValue(item.StartsAt), optionalTime(item.EndsAt)}}})
}

func newMaintenanceListCommand(runtime Runtime) *cobra.Command {
	var limit int32
	var cursor string
	command := &cobra.Command{
		Use: "list", Short: "List maintenance intervals", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			typedLimit, typedCursor, err := pageParams(limit, cursor)
			if err != nil {
				return err
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.ListMaintenanceWithResponse(cmd.Context(), &sdk.ListMaintenanceParams{Limit: typedLimit, Cursor: typedCursor})
			if err != nil {
				return remoteFailure("list maintenance", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			page := response.JSON200
			next := cursorValue(page.Page.NextCursor)
			rows := make([][]string, 0, len(page.Items))
			for _, item := range page.Items {
				rows = append(rows, []string{item.Id.String(), item.MonitorId.String(), item.Reason, timeValue(item.StartsAt), optionalTime(item.EndsAt), next})
			}
			return renderRemote(runtime, page, output.Table{Headers: []string{"ID", "MONITOR ID", "REASON", "STARTS AT", "ENDS AT", "NEXT CURSOR"}, Rows: rows})
		},
	}
	command.Flags().Int32Var(&limit, "limit", 50, "maximum records (1-100)")
	command.Flags().StringVar(&cursor, "cursor", "", "opaque cursor from the previous page")
	return command
}
