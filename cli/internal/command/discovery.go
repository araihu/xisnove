package command

import (
	"net/http"

	"github.com/araihu/xisnove/cli/internal/input"
	"github.com/araihu/xisnove/cli/internal/output"
	"github.com/araihu/xisnove/cli/internal/problem"
	"github.com/araihu/xisnove/sdk"
	"github.com/spf13/cobra"
)

func newDiscoveryCommand(runtime Runtime) *cobra.Command {
	command := &cobra.Command{Use: "discovery", Short: "Inspect and promote discovered targets"}
	command.AddCommand(newDiscoveryListCommand(runtime), newDiscoveryGetCommand(runtime), newDiscoveryPromoteCommand(runtime))
	return command
}

func newDiscoveryGetCommand(runtime Runtime) *cobra.Command {
	return &cobra.Command{
		Use: "get ID", Short: "Get a discovery candidate", Args: exactArgs(1, "discovery get requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("candidate ID", args[0])
			if err != nil {
				return err
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.GetDiscoveryCandidateWithResponse(cmd.Context(), id)
			if err != nil {
				return remoteFailure("get discovery candidate", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			return renderCandidate(runtime, response.JSON200)
		},
	}
}

func newDiscoveryPromoteCommand(runtime Runtime) *cobra.Command {
	var file, key string
	command := &cobra.Command{
		Use: "promote ID", Short: "Promote a discovery candidate with a generated-SDK request", Args: exactArgs(1, "discovery promote requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" {
				return problem.Usage("--file is required")
			}
			id, err := parseUUID("candidate ID", args[0])
			if err != nil {
				return err
			}
			var body sdk.PromoteDiscoveryCandidateJSONRequestBody
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
			response, err := client.PromoteDiscoveryCandidateWithResponse(cmd.Context(), id, &sdk.PromoteDiscoveryCandidateParams{IdempotencyKey: &resolved}, body, editors...)
			if err != nil {
				return remoteFailure("promote discovery candidate", err)
			}
			if response.StatusCode() != http.StatusCreated || response.JSON201 == nil {
				return responseProblem(response)
			}
			promotion := response.JSON201
			return renderRemote(runtime, promotion, output.Table{Headers: []string{"CANDIDATE ID", "MONITOR ID", "MONITOR NAME"}, Rows: [][]string{{promotion.Candidate.Id.String(), promotion.Monitor.Id.String(), promotion.Monitor.Name}}})
		},
	}
	addFileMutationFlags(command, &file, &key)
	return command
}

func renderCandidate(runtime Runtime, candidate *sdk.DiscoveryCandidate) error {
	return renderRemote(runtime, candidate, output.Table{Headers: []string{"ID", "NAME", "PROTOCOL", "STATE", "TARGET", "LOCATION ID"}, Rows: [][]string{{candidate.Id.String(), candidate.Name, string(candidate.Protocol), string(candidate.State), candidate.Target, candidate.LocationId.String()}}})
}

func newDiscoveryListCommand(runtime Runtime) *cobra.Command {
	var limit int32
	var cursor, state string
	command := &cobra.Command{
		Use:   "list",
		Short: "List discovery candidates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			typedLimit, typedCursor, err := pageParams(limit, cursor)
			if err != nil {
				return err
			}
			var typedState *sdk.DiscoveryCandidateState
			if state != "" {
				value := sdk.DiscoveryCandidateState(state)
				if !value.Valid() {
					return problem.Usage("--state must be pending, promoted, or ignored")
				}
				typedState = &value
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.ListDiscoveryCandidatesWithResponse(cmd.Context(), &sdk.ListDiscoveryCandidatesParams{Limit: typedLimit, Cursor: typedCursor, State: typedState})
			if err != nil {
				return remoteFailure("list discovery candidates", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			page := response.JSON200
			next := cursorValue(page.Page.NextCursor)
			rows := make([][]string, 0, len(page.Items))
			for _, candidate := range page.Items {
				rows = append(rows, []string{candidate.Id.String(), candidate.Name, string(candidate.Protocol), string(candidate.State), candidate.Target, candidate.LocationId.String(), next})
			}
			return renderRemote(runtime, page, output.Table{Headers: []string{"ID", "NAME", "PROTOCOL", "STATE", "TARGET", "LOCATION ID", "NEXT CURSOR"}, Rows: rows})
		},
	}
	command.Flags().Int32Var(&limit, "limit", 50, "maximum records (1-100)")
	command.Flags().StringVar(&cursor, "cursor", "", "opaque cursor from the previous page")
	command.Flags().StringVar(&state, "state", "", "candidate state: pending, promoted, or ignored")
	return command
}
