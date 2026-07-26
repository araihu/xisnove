package command

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/araihu/xisnove/cli/internal/idempotency"
	"github.com/araihu/xisnove/cli/internal/output"
	"github.com/araihu/xisnove/cli/internal/problem"
	"github.com/araihu/xisnove/sdk"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

type sdkResponse interface {
	StatusCode() int
	GetBody() []byte
}

func responseProblem(response sdkResponse) error {
	if response == nil {
		return problem.Local(http.StatusBadGateway, "Invalid server response", "the SDK returned no response", "invalid_response")
	}
	return problem.FromHTTP(response.StatusCode(), response.GetBody())
}

func remoteFailure(action string, err error) error {
	return problem.Local(http.StatusBadGateway, "Control-plane request failed", action+": "+err.Error(), "request_failed")
}

func renderRemote(runtime Runtime, value any, table output.Table) error {
	return (output.Renderer{Writer: runtime.Stdout, Format: output.Format(*runtime.OutputFormat)}).Render(value, table)
}

func parseUUID(kind, raw string) (uuid.UUID, error) {
	value, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, problem.Usage(kind + " must be an RFC 4122 UUID")
	}
	return value, nil
}

func pageParams(limit int32, cursor string) (*sdk.Limit, *sdk.Cursor, error) {
	if limit < 1 || limit > 100 {
		return nil, nil, problem.Usage("--limit must be between 1 and 100")
	}
	typedLimit := sdk.Limit(limit)
	var typedCursor *sdk.Cursor
	if cursor != "" {
		value := sdk.Cursor(cursor)
		typedCursor = &value
	}
	return &typedLimit, typedCursor, nil
}

func cursorValue(cursor *string) string {
	if cursor == nil {
		return ""
	}
	return *cursor
}

func timeValue(value time.Time) string {
	return value.UTC().Format(time.RFC3339)
}

func boolValue(value bool) string {
	return strconv.FormatBool(value)
}

func intValue(value int) string {
	return strconv.Itoa(value)
}

func int32Value(value int32) string {
	return strconv.FormatInt(int64(value), 10)
}

func optionalBool(value *bool) string {
	if value == nil {
		return "-"
	}
	return boolValue(*value)
}

func optionalTime(value *time.Time) string {
	if value == nil {
		return "-"
	}
	return timeValue(*value)
}

func exactArgs(count int, detail string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) != count {
			return problem.Usage(detail)
		}
		return nil
	}
}

type actionResult struct {
	Resource string `json:"resource"`
	ID       string `json:"id"`
	Action   string `json:"action"`
}

func renderAction(runtime Runtime, resource, id, action string) error {
	result := actionResult{Resource: resource, ID: id, Action: action}
	return renderRemote(runtime, result, output.Table{
		Headers: []string{"RESOURCE", "ID", "ACTION"},
		Rows:    [][]string{{resource, id, action}},
	})
}

func addFileMutationFlags(command *cobra.Command, file, key *string) {
	command.Flags().StringVarP(file, "file", "f", "", "generated-SDK request file, or - for stdin")
	addMutationFlag(command, key)
}

func addMutationFlag(command *cobra.Command, key *string) {
	command.Flags().StringVar(key, "idempotency-key", "", "stable retry key; omitted generates and reports one")
}

func mutationEditors(runtime Runtime, explicit string) (string, []sdk.RequestEditorFn, error) {
	key, err := (idempotencyPolicy(runtime)).Resolve(explicit)
	if err != nil {
		return "", nil, problem.Usage(err.Error())
	}
	return key, []sdk.RequestEditorFn{sdk.WithIdempotencyKey(key)}, nil
}

func idempotencyPolicy(runtime Runtime) idempotency.Policy {
	return idempotency.Policy{Diagnostics: runtime.Stderr}
}

func unsupportedAction(name string) error {
	return problem.Local(http.StatusNotImplemented, "Command unavailable", fmt.Sprintf("%s is not implemented by this CLI build", name), "command_unavailable")
}
