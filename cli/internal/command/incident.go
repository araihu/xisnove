package command

import (
	"net/http"

	"github.com/araihu/xisnove/cli/internal/output"
	"github.com/araihu/xisnove/cli/internal/problem"
	"github.com/araihu/xisnove/sdk"
	"github.com/spf13/cobra"
)

func newIncidentCommand(runtime Runtime) *cobra.Command {
	command := &cobra.Command{Use: "incident", Short: "Inspect incidents and their immutable events"}
	command.AddCommand(newIncidentListCommand(runtime), newIncidentGetCommand(runtime), newIncidentEventsCommand(runtime))
	return command
}

func newIncidentGetCommand(runtime Runtime) *cobra.Command {
	return &cobra.Command{
		Use: "get ID", Short: "Get an incident", Args: exactArgs(1, "incident get requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("incident ID", args[0])
			if err != nil {
				return err
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.GetIncidentWithResponse(cmd.Context(), id)
			if err != nil {
				return remoteFailure("get incident", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			return renderIncident(runtime, response.JSON200)
		},
	}
}

func newIncidentEventsCommand(runtime Runtime) *cobra.Command {
	var limit int32
	var cursor string
	command := &cobra.Command{
		Use: "events ID", Short: "List immutable incident events", Args: exactArgs(1, "incident events requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("incident ID", args[0])
			if err != nil {
				return err
			}
			typedLimit, typedCursor, err := pageParams(limit, cursor)
			if err != nil {
				return err
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.ListIncidentEventsWithResponse(cmd.Context(), id, &sdk.ListIncidentEventsParams{Limit: typedLimit, Cursor: typedCursor})
			if err != nil {
				return remoteFailure("list incident events", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			page := response.JSON200
			next := cursorValue(page.Page.NextCursor)
			rows := make([][]string, 0, len(page.Items))
			for _, event := range page.Items {
				rows = append(rows, []string{event.Id.String(), string(event.Action), string(event.PreviousState), string(event.State), string(event.Severity), timeValue(event.OccurredAt), next})
			}
			return renderRemote(runtime, page, output.Table{Headers: []string{"ID", "ACTION", "PREVIOUS", "STATE", "SEVERITY", "OCCURRED AT", "NEXT CURSOR"}, Rows: rows})
		},
	}
	command.Flags().Int32Var(&limit, "limit", 50, "maximum records (1-100)")
	command.Flags().StringVar(&cursor, "cursor", "", "opaque cursor from the previous page")
	return command
}

func renderIncident(runtime Runtime, incident *sdk.Incident) error {
	return renderRemote(runtime, incident, output.Table{Headers: []string{"ID", "MONITOR ID", "STATE", "SEVERITY", "OPENED AT", "RESOLVED AT"}, Rows: [][]string{{incident.Id.String(), incident.MonitorId.String(), string(incident.State), string(incident.Severity), timeValue(incident.OpenedAt), optionalTime(incident.ResolvedAt)}}})
}

func newIncidentListCommand(runtime Runtime) *cobra.Command {
	var limit int32
	var cursor, state string
	command := &cobra.Command{
		Use:   "list",
		Short: "List incidents",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			typedLimit, typedCursor, err := pageParams(limit, cursor)
			if err != nil {
				return err
			}
			var typedState *sdk.ListIncidentsParamsState
			if state != "" {
				value := sdk.ListIncidentsParamsState(state)
				if !value.Valid() {
					return problem.Usage("--state must be open or resolved")
				}
				typedState = &value
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.ListIncidentsWithResponse(cmd.Context(), &sdk.ListIncidentsParams{Limit: typedLimit, Cursor: typedCursor, State: typedState})
			if err != nil {
				return remoteFailure("list incidents", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			page := response.JSON200
			next := cursorValue(page.Page.NextCursor)
			rows := make([][]string, 0, len(page.Items))
			for _, incident := range page.Items {
				rows = append(rows, []string{incident.Id.String(), incident.MonitorId.String(), string(incident.State), string(incident.Severity), timeValue(incident.OpenedAt), next})
			}
			return renderRemote(runtime, page, output.Table{Headers: []string{"ID", "MONITOR ID", "STATE", "SEVERITY", "OPENED AT", "NEXT CURSOR"}, Rows: rows})
		},
	}
	command.Flags().Int32Var(&limit, "limit", 50, "maximum records (1-100)")
	command.Flags().StringVar(&cursor, "cursor", "", "opaque cursor from the previous page")
	command.Flags().StringVar(&state, "state", "", "incident state: open or resolved")
	return command
}
