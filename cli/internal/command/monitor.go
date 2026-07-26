package command

import (
	"net/http"

	"github.com/araihu/xisnove/cli/internal/input"
	"github.com/araihu/xisnove/cli/internal/output"
	"github.com/araihu/xisnove/cli/internal/problem"
	"github.com/araihu/xisnove/sdk"
	"github.com/spf13/cobra"
)

func newMonitorCommand(runtime Runtime) *cobra.Command {
	command := &cobra.Command{Use: "monitor", Short: "Manage monitors"}
	command.AddCommand(
		newMonitorListCommand(runtime),
		newMonitorGetCommand(runtime),
		newMonitorCreateCommand(runtime),
		newMonitorUpdateCommand(runtime),
		newMonitorDisableCommand(runtime),
		newMonitorHealthCommand(runtime),
		newMonitorIncidentCommand(runtime),
	)
	return command
}

func newMonitorGetCommand(runtime Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "get ID",
		Short: "Get a monitor",
		Args:  exactArgs(1, "monitor get requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("monitor ID", args[0])
			if err != nil {
				return err
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.GetMonitorWithResponse(cmd.Context(), id)
			if err != nil {
				return remoteFailure("get monitor", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			return renderMonitor(runtime, response.JSON200)
		},
	}
}

func newMonitorCreateCommand(runtime Runtime) *cobra.Command {
	var file, idempotencyKey string
	command := &cobra.Command{
		Use:   "create",
		Short: "Create a monitor from a generated-SDK JSON or YAML request",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if file == "" {
				return problem.Usage("--file is required")
			}
			var body sdk.CreateMonitorJSONRequestBody
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
			response, err := client.CreateMonitorWithResponse(cmd.Context(), body, editors...)
			if err != nil {
				return remoteFailure("create monitor", err)
			}
			if response.StatusCode() != http.StatusCreated || response.JSON201 == nil {
				return responseProblem(response)
			}
			return renderMonitor(runtime, response.JSON201)
		},
	}
	command.Flags().StringVarP(&file, "file", "f", "", "generated-SDK request file, or - for stdin")
	command.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "stable retry key; omitted generates and reports one")
	return command
}

func newMonitorUpdateCommand(runtime Runtime) *cobra.Command {
	var file, idempotencyKey string
	command := &cobra.Command{
		Use:   "update ID",
		Short: "Update a monitor from a generated-SDK JSON or YAML request",
		Args:  exactArgs(1, "monitor update requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" {
				return problem.Usage("--file is required")
			}
			id, err := parseUUID("monitor ID", args[0])
			if err != nil {
				return err
			}
			var body sdk.UpdateMonitorJSONRequestBody
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
			response, err := client.UpdateMonitorWithResponse(cmd.Context(), id, &sdk.UpdateMonitorParams{IdempotencyKey: &key}, body, editors...)
			if err != nil {
				return remoteFailure("update monitor", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			return renderMonitor(runtime, response.JSON200)
		},
	}
	command.Flags().StringVarP(&file, "file", "f", "", "generated-SDK request file, or - for stdin")
	command.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "stable retry key; omitted generates and reports one")
	return command
}

func newMonitorDisableCommand(runtime Runtime) *cobra.Command {
	var idempotencyKey string
	command := &cobra.Command{
		Use:   "disable ID",
		Short: "Disable a monitor",
		Args:  exactArgs(1, "monitor disable requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("monitor ID", args[0])
			if err != nil {
				return err
			}
			_, editors, err := mutationEditors(runtime, idempotencyKey)
			if err != nil {
				return err
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.DisableMonitorWithResponse(cmd.Context(), id, editors...)
			if err != nil {
				return remoteFailure("disable monitor", err)
			}
			if response.StatusCode() != http.StatusNoContent {
				return responseProblem(response)
			}
			return renderAction(runtime, "monitor", id.String(), "disabled")
		},
	}
	command.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "stable retry key; omitted generates and reports one")
	return command
}

func newMonitorHealthCommand(runtime Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "health ID",
		Short: "Show projected monitor health",
		Args:  exactArgs(1, "monitor health requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("monitor ID", args[0])
			if err != nil {
				return err
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.GetMonitorHealthWithResponse(cmd.Context(), id)
			if err != nil {
				return remoteFailure("get monitor health", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			health := response.JSON200
			return renderRemote(runtime, health, output.Table{
				Headers: []string{"MONITOR ID", "STATE", "LOCATIONS", "LAST TRANSITION"},
				Rows:    [][]string{{health.MonitorId.String(), string(health.State), intValue(len(health.Locations)), timeValue(health.LastTransitionAt)}},
			})
		},
	}
}

func newMonitorIncidentCommand(runtime Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "incident ID",
		Short: "Show the active incident for a monitor",
		Args:  exactArgs(1, "monitor incident requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("monitor ID", args[0])
			if err != nil {
				return err
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.GetActiveMonitorIncidentWithResponse(cmd.Context(), id)
			if err != nil {
				return remoteFailure("get active monitor incident", err)
			}
			if response.StatusCode() == http.StatusNoContent {
				return renderRemote(runtime, nil, output.Table{Headers: []string{"ID", "STATE", "SEVERITY", "OPENED AT"}})
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			incident := response.JSON200
			return renderRemote(runtime, incident, output.Table{
				Headers: []string{"ID", "STATE", "SEVERITY", "OPENED AT"},
				Rows:    [][]string{{incident.Id.String(), string(incident.State), string(incident.Severity), timeValue(incident.OpenedAt)}},
			})
		},
	}
}

func renderMonitor(runtime Runtime, monitor *sdk.Monitor) error {
	return renderRemote(runtime, monitor, output.Table{
		Headers: []string{"ID", "NAME", "KIND", "ENABLED", "LOCATION ID"},
		Rows:    [][]string{{monitor.Id.String(), monitor.Name, string(monitor.Kind), boolValue(monitor.Enabled), monitor.LocationId.String()}},
	})
}

func newMonitorListCommand(runtime Runtime) *cobra.Command {
	var limit int32
	var cursor string
	command := &cobra.Command{
		Use:   "list",
		Short: "List monitors",
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
			response, err := client.ListMonitorsWithResponse(cmd.Context(), &sdk.ListMonitorsParams{Limit: typedLimit, Cursor: typedCursor})
			if err != nil {
				return remoteFailure("list monitors", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			page := response.JSON200
			nextCursor := cursorValue(page.NextCursor)
			rows := make([][]string, 0, len(page.Items))
			for _, monitor := range page.Items {
				rows = append(rows, []string{
					monitor.Id.String(), monitor.Name, string(monitor.Kind), boolValue(monitor.Enabled), monitor.LocationId.String(), nextCursor,
				})
			}
			return renderRemote(runtime, page, output.Table{
				Headers: []string{"ID", "NAME", "KIND", "ENABLED", "LOCATION ID", "NEXT CURSOR"},
				Rows:    rows,
			})
		},
	}
	command.Flags().Int32Var(&limit, "limit", 50, "maximum records (1-100)")
	command.Flags().StringVar(&cursor, "cursor", "", "opaque cursor from the previous page")
	return command
}
