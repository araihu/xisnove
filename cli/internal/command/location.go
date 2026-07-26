package command

import (
	"net/http"

	"github.com/araihu/xisnove/cli/internal/input"
	"github.com/araihu/xisnove/cli/internal/output"
	"github.com/araihu/xisnove/cli/internal/problem"
	"github.com/araihu/xisnove/sdk"
	"github.com/spf13/cobra"
)

func newLocationCommand(runtime Runtime) *cobra.Command {
	command := &cobra.Command{Use: "location", Short: "Manage probe locations"}
	command.AddCommand(newLocationListCommand(runtime), newLocationGetCommand(runtime), newLocationCreateCommand(runtime), newLocationUpdateCommand(runtime), newLocationDisableCommand(runtime))
	return command
}

func newLocationGetCommand(runtime Runtime) *cobra.Command {
	return &cobra.Command{
		Use: "get ID", Short: "Get a location", Args: exactArgs(1, "location get requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("location ID", args[0])
			if err != nil {
				return err
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.GetLocationWithResponse(cmd.Context(), id)
			if err != nil {
				return remoteFailure("get location", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			return renderLocation(runtime, response.JSON200)
		},
	}
}

func newLocationCreateCommand(runtime Runtime) *cobra.Command {
	var file, idempotencyKey string
	command := &cobra.Command{
		Use: "create", Short: "Create a location from a generated-SDK request", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if file == "" {
				return problem.Usage("--file is required")
			}
			var body sdk.CreateLocationJSONRequestBody
			if err := input.DecodeFile(file, runtime.Stdin, &body); err != nil {
				return problem.Usage(err.Error())
			}
			_, editors, err := mutationEditors(runtime, idempotencyKey)
			if err != nil {
				return err
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.CreateLocationWithResponse(cmd.Context(), body, editors...)
			if err != nil {
				return remoteFailure("create location", err)
			}
			if response.StatusCode() != http.StatusCreated || response.JSON201 == nil {
				return responseProblem(response)
			}
			return renderLocation(runtime, response.JSON201)
		},
	}
	addFileMutationFlags(command, &file, &idempotencyKey)
	return command
}

func newLocationUpdateCommand(runtime Runtime) *cobra.Command {
	var file, idempotencyKey string
	command := &cobra.Command{
		Use: "update ID", Short: "Update a location from a generated-SDK request", Args: exactArgs(1, "location update requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" {
				return problem.Usage("--file is required")
			}
			id, err := parseUUID("location ID", args[0])
			if err != nil {
				return err
			}
			var body sdk.UpdateLocationJSONRequestBody
			if err := input.DecodeFile(file, runtime.Stdin, &body); err != nil {
				return problem.Usage(err.Error())
			}
			key, editors, err := mutationEditors(runtime, idempotencyKey)
			if err != nil {
				return err
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.UpdateLocationWithResponse(cmd.Context(), id, &sdk.UpdateLocationParams{IdempotencyKey: &key}, body, editors...)
			if err != nil {
				return remoteFailure("update location", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			return renderLocation(runtime, response.JSON200)
		},
	}
	addFileMutationFlags(command, &file, &idempotencyKey)
	return command
}

func newLocationDisableCommand(runtime Runtime) *cobra.Command {
	var key string
	command := &cobra.Command{
		Use: "disable ID", Short: "Disable a location", Args: exactArgs(1, "location disable requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("location ID", args[0])
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
			response, err := client.DisableLocationWithResponse(cmd.Context(), id, editors...)
			if err != nil {
				return remoteFailure("disable location", err)
			}
			if response.StatusCode() != http.StatusNoContent {
				return responseProblem(response)
			}
			return renderAction(runtime, "location", id.String(), "disabled")
		},
	}
	addMutationFlag(command, &key)
	return command
}

func renderLocation(runtime Runtime, location *sdk.Location) error {
	return renderRemote(runtime, location, output.Table{Headers: []string{"ID", "NAME", "ENABLED", "UPDATED AT"}, Rows: [][]string{{location.Id.String(), location.Name, optionalBool(location.Enabled), optionalTime(location.UpdatedAt)}}})
}

func newLocationListCommand(runtime Runtime) *cobra.Command {
	var limit int32
	var cursor string
	command := &cobra.Command{
		Use:   "list",
		Short: "List locations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			typedLimit, typedCursor, err := pageParams(limit, cursor)
			if err != nil {
				return err
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.ListLocationsWithResponse(cmd.Context(), &sdk.ListLocationsParams{Limit: typedLimit, Cursor: typedCursor})
			if err != nil {
				return remoteFailure("list locations", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			page := response.JSON200
			next := cursorValue(page.NextCursor)
			rows := make([][]string, 0, len(page.Items))
			for _, location := range page.Items {
				rows = append(rows, []string{location.Id.String(), location.Name, optionalBool(location.Enabled), optionalTime(location.UpdatedAt), next})
			}
			return renderRemote(runtime, page, output.Table{Headers: []string{"ID", "NAME", "ENABLED", "UPDATED AT", "NEXT CURSOR"}, Rows: rows})
		},
	}
	command.Flags().Int32Var(&limit, "limit", 50, "maximum records (1-100)")
	command.Flags().StringVar(&cursor, "cursor", "", "opaque cursor from the previous page")
	return command
}
