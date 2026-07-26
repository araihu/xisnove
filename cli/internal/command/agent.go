package command

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/araihu/xisnove/cli/internal/config"
	"github.com/araihu/xisnove/cli/internal/input"
	"github.com/araihu/xisnove/cli/internal/output"
	"github.com/araihu/xisnove/cli/internal/problem"
	"github.com/araihu/xisnove/sdk"
	"github.com/spf13/cobra"
)

func newAgentCommand(runtime Runtime) *cobra.Command {
	command := &cobra.Command{Use: "agent", Short: "Manage probe agents"}
	command.AddCommand(newAgentListCommand(runtime), newAgentGetCommand(runtime), newAgentUpdateCommand(runtime), newAgentRevokeCommand(runtime), newAgentRotateCredentialCommand(runtime), newAgentRevokeCredentialGenerationCommand(runtime), newAgentEnrollmentTokenCommand(runtime))
	return command
}

func newAgentGetCommand(runtime Runtime) *cobra.Command {
	return &cobra.Command{
		Use: "get ID", Short: "Get an agent", Args: exactArgs(1, "agent get requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("agent ID", args[0])
			if err != nil {
				return err
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.GetAgentWithResponse(cmd.Context(), id)
			if err != nil {
				return remoteFailure("get agent", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			return renderAgent(runtime, response.JSON200)
		},
	}
}

func newAgentUpdateCommand(runtime Runtime) *cobra.Command {
	var file, key string
	command := &cobra.Command{
		Use: "update ID", Short: "Update an agent from a generated-SDK request", Args: exactArgs(1, "agent update requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" {
				return problem.Usage("--file is required")
			}
			id, err := parseUUID("agent ID", args[0])
			if err != nil {
				return err
			}
			var body sdk.UpdateAgentJSONRequestBody
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
			response, err := client.UpdateAgentWithResponse(cmd.Context(), id, &sdk.UpdateAgentParams{IdempotencyKey: &resolved}, body, editors...)
			if err != nil {
				return remoteFailure("update agent", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			return renderAgent(runtime, response.JSON200)
		},
	}
	addFileMutationFlags(command, &file, &key)
	return command
}

func newAgentRevokeCommand(runtime Runtime) *cobra.Command {
	var key string
	command := &cobra.Command{
		Use: "revoke ID", Short: "Revoke an agent and all of its credentials", Args: exactArgs(1, "agent revoke requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("agent ID", args[0])
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
			response, err := client.RevokeAgentWithResponse(cmd.Context(), id, editors...)
			if err != nil {
				return remoteFailure("revoke agent", err)
			}
			if response.StatusCode() != http.StatusNoContent {
				return responseProblem(response)
			}
			return renderAction(runtime, "agent", id.String(), "revoked")
		},
	}
	addMutationFlag(command, &key)
	return command
}

type enrollmentTokenResult struct {
	ExpiresAt  string               `json:"expiresAt"`
	Credential config.CredentialRef `json:"credential"`
}

type rotatedAgentCredentialResult struct {
	AgentID    string               `json:"agentId"`
	Generation int64                `json:"credentialGeneration"`
	Credential config.CredentialRef `json:"credential"`
}

func newAgentRotateCredentialCommand(runtime Runtime) *cobra.Command {
	var storeFile, key string
	command := &cobra.Command{
		Use: "rotate-credential ID", Short: "Rotate an Agent credential and store its one-time plaintext in a private file", Args: exactArgs(1, "agent rotate-credential requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if storeFile == "" {
				return problem.Usage("--store-file is required")
			}
			ref := config.CredentialRef{Mode: config.CredentialFile, Reference: storeFile}
			if err := ref.Validate(); err != nil {
				return problem.Usage(err.Error())
			}
			id, err := parseUUID("agent ID", args[0])
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
			response, err := client.RotateAgentCredentialWithResponse(cmd.Context(), id, &sdk.RotateAgentCredentialParams{IdempotencyKey: &resolved}, editors...)
			if err != nil {
				return remoteFailure("rotate agent credential", err)
			}
			if response.StatusCode() != http.StatusCreated || response.JSON201 == nil {
				return responseProblem(response)
			}
			if response.JSON201.Credential == nil || strings.TrimSpace(*response.JSON201.Credential) == "" {
				return problem.Local(http.StatusBadGateway, "Invalid server response", "rotated Agent credential is missing", "invalid_response")
			}
			if err := runtime.Credentials.Store(ref, *response.JSON201.Credential); err != nil {
				return localFailure("store rotated agent credential", err)
			}
			result := rotatedAgentCredentialResult{AgentID: response.JSON201.AgentId.String(), Generation: response.JSON201.CredentialGeneration, Credential: ref}
			return renderRemote(runtime, result, output.Table{Headers: []string{"AGENT ID", "GENERATION", "CREDENTIAL", "REFERENCE"}, Rows: [][]string{{result.AgentID, strconv.FormatInt(result.Generation, 10), string(ref.Mode), ref.Reference}}})
		},
	}
	command.Flags().StringVar(&storeFile, "store-file", "", "absolute private file for the one-time credential")
	addMutationFlag(command, &key)
	return command
}

func newAgentRevokeCredentialGenerationCommand(runtime Runtime) *cobra.Command {
	var key string
	command := &cobra.Command{
		Use: "revoke-generation ID GENERATION", Short: "Revoke one superseded Agent credential generation", Args: exactArgs(2, "agent revoke-generation requires an ID and generation"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("agent ID", args[0])
			if err != nil {
				return err
			}
			generation, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil || generation < 1 {
				return problem.Usage("credential generation must be a positive integer")
			}
			_, editors, err := mutationEditors(runtime, key)
			if err != nil {
				return err
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.RevokeAgentCredentialGenerationWithResponse(cmd.Context(), id, sdk.AgentCredentialGeneration(generation), editors...)
			if err != nil {
				return remoteFailure("revoke agent credential generation", err)
			}
			if response.StatusCode() != http.StatusNoContent {
				return responseProblem(response)
			}
			return renderAction(runtime, "agent-credential-generation", id.String()+":"+strconv.FormatInt(generation, 10), "revoked")
		},
	}
	addMutationFlag(command, &key)
	return command
}

func newAgentEnrollmentTokenCommand(runtime Runtime) *cobra.Command {
	var file, storeFile, key string
	command := &cobra.Command{
		Use: "enrollment-token", Short: "Create an Agent enrollment token and store it in a private file", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if file == "" || storeFile == "" {
				return problem.Usage("--file and --store-file are required")
			}
			ref := config.CredentialRef{Mode: config.CredentialFile, Reference: storeFile}
			if err := ref.Validate(); err != nil {
				return problem.Usage(err.Error())
			}
			var body sdk.CreateAgentEnrollmentTokenJSONRequestBody
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
			response, err := client.CreateAgentEnrollmentTokenWithResponse(cmd.Context(), &sdk.CreateAgentEnrollmentTokenParams{IdempotencyKey: &resolved}, body, editors...)
			if err != nil {
				return remoteFailure("create agent enrollment token", err)
			}
			if response.StatusCode() != http.StatusCreated || response.JSON201 == nil {
				return responseProblem(response)
			}
			if err := runtime.Credentials.Store(ref, response.JSON201.Token); err != nil {
				return localFailure("store agent enrollment token", err)
			}
			result := enrollmentTokenResult{ExpiresAt: timeValue(response.JSON201.ExpiresAt), Credential: ref}
			return renderRemote(runtime, result, output.Table{Headers: []string{"EXPIRES AT", "CREDENTIAL", "REFERENCE"}, Rows: [][]string{{result.ExpiresAt, string(ref.Mode), ref.Reference}}})
		},
	}
	command.Flags().StringVarP(&file, "file", "f", "", "generated-SDK request file, or - for stdin")
	command.Flags().StringVar(&storeFile, "store-file", "", "absolute private file for the one-time token")
	addMutationFlag(command, &key)
	return command
}

func renderAgent(runtime Runtime, agent *sdk.Agent) error {
	capabilities := make([]string, len(agent.Capabilities))
	for index, capability := range agent.Capabilities {
		capabilities[index] = string(capability)
	}
	return renderRemote(runtime, agent, output.Table{Headers: []string{"ID", "NAME", "ENABLED", "LOCATION ID", "CAPABILITIES", "LAST SEEN"}, Rows: [][]string{{agent.Id.String(), agent.Name, boolValue(agent.Enabled), agent.LocationId.String(), strings.Join(capabilities, ","), optionalTime(agent.LastSeenAt)}}})
}

func newAgentListCommand(runtime Runtime) *cobra.Command {
	var limit int32
	var cursor string
	command := &cobra.Command{
		Use:   "list",
		Short: "List agents",
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
			response, err := client.ListAgentsWithResponse(cmd.Context(), &sdk.ListAgentsParams{Limit: typedLimit, Cursor: typedCursor})
			if err != nil {
				return remoteFailure("list agents", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			page := response.JSON200
			next := cursorValue(page.Page.NextCursor)
			rows := make([][]string, 0, len(page.Items))
			for _, agent := range page.Items {
				capabilities := make([]string, len(agent.Capabilities))
				for index, capability := range agent.Capabilities {
					capabilities[index] = string(capability)
				}
				rows = append(rows, []string{agent.Id.String(), agent.Name, boolValue(agent.Enabled), agent.LocationId.String(), strings.Join(capabilities, ","), optionalTime(agent.LastSeenAt), next})
			}
			return renderRemote(runtime, page, output.Table{Headers: []string{"ID", "NAME", "ENABLED", "LOCATION ID", "CAPABILITIES", "LAST SEEN", "NEXT CURSOR"}, Rows: rows})
		},
	}
	command.Flags().Int32Var(&limit, "limit", 50, "maximum records (1-100)")
	command.Flags().StringVar(&cursor, "cursor", "", "opaque cursor from the previous page")
	return command
}
