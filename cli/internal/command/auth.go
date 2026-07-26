package command

import (
	"net/http"
	"os"
	"strings"

	"github.com/araihu/xisnove/cli/internal/config"
	"github.com/araihu/xisnove/cli/internal/input"
	"github.com/araihu/xisnove/cli/internal/output"
	"github.com/araihu/xisnove/cli/internal/problem"
	"github.com/araihu/xisnove/sdk"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/spf13/cobra"
)

type loginResult struct {
	Profile    string               `json:"profile"`
	ExpiresAt  string               `json:"expiresAt"`
	Credential config.CredentialRef `json:"credential"`
}

func newAuthCommand(runtime Runtime) *cobra.Command {
	command := &cobra.Command{Use: "auth", Short: "Manage administrator authentication and API tokens"}
	tokens := &cobra.Command{Use: "token", Short: "Manage scoped API tokens"}
	tokens.AddCommand(newAPITokenListCommand(runtime), newAPITokenGetCommand(runtime), newAPITokenCreateCommand(runtime), newAPITokenUpdateCommand(runtime), newAPITokenRevokeCommand(runtime))
	command.AddCommand(newLoginCommand(runtime), newLogoutCommand(runtime), tokens)
	return command
}

func newAPITokenListCommand(runtime Runtime) *cobra.Command {
	var limit int32
	var cursor string
	command := &cobra.Command{
		Use: "list", Short: "List scoped API tokens", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			typedLimit, typedCursor, err := pageParams(limit, cursor)
			if err != nil {
				return err
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.ListAPITokensWithResponse(cmd.Context(), &sdk.ListAPITokensParams{Limit: typedLimit, Cursor: typedCursor})
			if err != nil {
				return remoteFailure("list API tokens", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			page := response.JSON200
			next := cursorValue(page.NextCursor)
			rows := make([][]string, 0, len(page.Items))
			for index := range page.Items {
				rows = append(rows, apiTokenRow(&page.Items[index], next))
			}
			return renderRemote(runtime, page, output.Table{Headers: []string{"ID", "NAME", "SCOPES", "EXPIRES AT", "REVOKED AT", "NEXT CURSOR"}, Rows: rows})
		},
	}
	command.Flags().Int32Var(&limit, "limit", 50, "maximum records (1-100)")
	command.Flags().StringVar(&cursor, "cursor", "", "opaque cursor from the previous page")
	return command
}

func newAPITokenGetCommand(runtime Runtime) *cobra.Command {
	return &cobra.Command{
		Use: "get ID", Short: "Get scoped API-token metadata", Args: exactArgs(1, "auth token get requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("API token ID", args[0])
			if err != nil {
				return err
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.GetAPITokenWithResponse(cmd.Context(), id)
			if err != nil {
				return remoteFailure("get API token", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			return renderAPIToken(runtime, response.JSON200)
		},
	}
}

func newAPITokenCreateCommand(runtime Runtime) *cobra.Command {
	var file, storeProfile, key string
	command := &cobra.Command{
		Use: "create", Short: "Create a scoped API token and store its one-time plaintext in a profile", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if file == "" || storeProfile == "" {
				return problem.Usage("--file and --store-profile are required")
			}
			cfg, err := loadConfig(runtime)
			if err != nil {
				return err
			}
			target, ok := cfg.Profiles[storeProfile]
			if !ok {
				return problem.Usage("--store-profile must name an existing profile")
			}
			if err := requireWritableCredential(target.Credential, "storing a created API token"); err != nil {
				return err
			}
			var body sdk.CreateAPITokenJSONRequestBody
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
			response, err := client.CreateAPITokenWithResponse(cmd.Context(), &sdk.CreateAPITokenParams{IdempotencyKey: &resolved}, body, editors...)
			if err != nil {
				return remoteFailure("create API token", err)
			}
			if response.StatusCode() != http.StatusCreated || response.JSON201 == nil {
				return responseProblem(response)
			}
			if err := runtime.Credentials.Store(target.Credential, response.JSON201.Token); err != nil {
				return localFailure("store one-time API token", err)
			}
			return renderAPIToken(runtime, &response.JSON201.ApiToken)
		},
	}
	command.Flags().StringVarP(&file, "file", "f", "", "generated-SDK request file, or - for stdin")
	command.Flags().StringVar(&storeProfile, "store-profile", "", "existing profile whose credential reference receives the one-time token")
	addMutationFlag(command, &key)
	return command
}

func newAPITokenUpdateCommand(runtime Runtime) *cobra.Command {
	var file, key string
	command := &cobra.Command{
		Use: "update ID", Short: "Update scoped API-token metadata", Args: exactArgs(1, "auth token update requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" {
				return problem.Usage("--file is required")
			}
			id, err := parseUUID("API token ID", args[0])
			if err != nil {
				return err
			}
			var body sdk.UpdateAPITokenJSONRequestBody
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
			response, err := client.UpdateAPITokenWithResponse(cmd.Context(), id, &sdk.UpdateAPITokenParams{IdempotencyKey: &resolved}, body, editors...)
			if err != nil {
				return remoteFailure("update API token", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			return renderAPIToken(runtime, response.JSON200)
		},
	}
	addFileMutationFlags(command, &file, &key)
	return command
}

func newAPITokenRevokeCommand(runtime Runtime) *cobra.Command {
	var key string
	command := &cobra.Command{
		Use: "revoke ID", Short: "Revoke a scoped API token", Args: exactArgs(1, "auth token revoke requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("API token ID", args[0])
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
			response, err := client.RevokeAPITokenWithResponse(cmd.Context(), id, editors...)
			if err != nil {
				return remoteFailure("revoke API token", err)
			}
			if response.StatusCode() != http.StatusNoContent {
				return responseProblem(response)
			}
			return renderAction(runtime, "api-token", id.String(), "revoked")
		},
	}
	addMutationFlag(command, &key)
	return command
}

func apiTokenRow(token *sdk.APIToken, next string) []string {
	scopes := make([]string, len(token.Scopes))
	for index, scope := range token.Scopes {
		scopes[index] = string(scope)
	}
	return []string{token.Id.String(), token.Name, strings.Join(scopes, ","), optionalTime(token.ExpiresAt), optionalTime(token.RevokedAt), next}
}

func renderAPIToken(runtime Runtime, token *sdk.APIToken) error {
	return renderRemote(runtime, token, output.Table{Headers: []string{"ID", "NAME", "SCOPES", "EXPIRES AT", "REVOKED AT"}, Rows: [][]string{apiTokenRow(token, "")[:5]}})
}

func newLogoutCommand(runtime Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Revoke the current session and remove its writable credential",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, profile, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			if err := requireWritableCredential(profile.Credential, "logout"); err != nil {
				return err
			}
			response, err := client.RevokeSessionWithResponse(cmd.Context())
			if err != nil {
				return remoteFailure("revoke administrator session", err)
			}
			if response.StatusCode() != http.StatusNoContent {
				return responseProblem(response)
			}
			if err := runtime.Credentials.Delete(profile.Credential); err != nil {
				return localFailure("delete revoked session credential", err)
			}
			return renderAction(runtime, "session", profile.Name, "revoked")
		},
	}
}

func newLoginCommand(runtime Runtime) *cobra.Command {
	var email, passwordEnv string
	var passwordStdin bool
	command := &cobra.Command{
		Use:   "login",
		Short: "Create and securely store an administrator session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(email) == "" {
				return problem.Usage("--email is required")
			}
			if passwordStdin == (passwordEnv != "") {
				return problem.Usage("choose exactly one of --password-stdin or --password-env")
			}
			var password string
			var err error
			if passwordStdin {
				password, err = input.ReadSecretLine(runtime.Stdin)
			} else {
				value, ok := os.LookupEnv(passwordEnv)
				if !ok {
					return problem.Usage("password environment variable is not set")
				}
				password, err = input.ReadSecretLine(strings.NewReader(value))
			}
			if err != nil {
				return problem.Usage(err.Error())
			}
			client, profile, err := runtime.OpenClient(false)
			if err != nil {
				return localFailure("open profile", err)
			}
			if err := requireWritableCredential(profile.Credential, "login"); err != nil {
				return err
			}
			response, err := client.CreateSessionWithResponse(cmd.Context(), sdk.CreateSessionJSONRequestBody{
				Email:    openapi_types.Email(email),
				Password: &password,
			})
			if err != nil {
				return remoteFailure("create administrator session", err)
			}
			if response.StatusCode() != http.StatusCreated || response.JSON201 == nil {
				return responseProblem(response)
			}
			if err := runtime.Credentials.Store(profile.Credential, response.JSON201.Token); err != nil {
				return localFailure("store administrator session", err)
			}
			result := loginResult{
				Profile:    profile.Name,
				ExpiresAt:  timeValue(response.JSON201.ExpiresAt),
				Credential: profile.Credential,
			}
			return renderRemote(runtime, result, output.Table{
				Headers: []string{"PROFILE", "EXPIRES AT", "CREDENTIAL", "REFERENCE"},
				Rows:    [][]string{{result.Profile, result.ExpiresAt, string(result.Credential.Mode), result.Credential.Reference}},
			})
		},
	}
	command.Flags().StringVar(&email, "email", "", "administrator email")
	command.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read the password from stdin")
	command.Flags().StringVar(&passwordEnv, "password-env", "", "read the password from the named environment variable")
	return command
}

func requireWritableCredential(ref config.CredentialRef, purpose string) error {
	if ref.Mode == config.CredentialEnv {
		return problem.Usage(purpose + " requires a keyring or file credential; env mode is read-only")
	}
	return nil
}
