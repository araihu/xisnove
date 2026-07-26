package command

import (
	"net/http"

	"github.com/araihu/xisnove/cli/internal/input"
	"github.com/araihu/xisnove/cli/internal/output"
	"github.com/araihu/xisnove/cli/internal/problem"
	"github.com/araihu/xisnove/sdk"
	"github.com/spf13/cobra"
)

func newNotificationCommand(runtime Runtime) *cobra.Command {
	command := &cobra.Command{Use: "notification", Short: "Manage notification channels, routes, and deliveries"}
	channel := &cobra.Command{Use: "channel", Short: "Manage notification channels"}
	channel.AddCommand(newNotificationChannelListCommand(runtime), newNotificationChannelGetCommand(runtime), newNotificationChannelCreateCommand(runtime), newNotificationChannelUpdateCommand(runtime), newNotificationChannelDisableCommand(runtime))
	route := &cobra.Command{Use: "route", Short: "Manage notification routes"}
	route.AddCommand(newNotificationRouteListCommand(runtime), newNotificationRouteGetCommand(runtime), newNotificationRouteCreateCommand(runtime), newNotificationRouteUpdateCommand(runtime), newNotificationRouteDisableCommand(runtime))
	delivery := &cobra.Command{Use: "delivery", Short: "Inspect notification deliveries"}
	delivery.AddCommand(newNotificationDeliveryListCommand(runtime), newNotificationDeliveryGetCommand(runtime), newNotificationDeliveryReplayCommand(runtime))
	command.AddCommand(channel, route, delivery)
	return command
}

func newNotificationChannelGetCommand(runtime Runtime) *cobra.Command {
	return &cobra.Command{
		Use: "get ID", Short: "Get a redacted notification channel", Args: exactArgs(1, "notification channel get requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("channel ID", args[0])
			if err != nil {
				return err
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.GetNotificationChannelWithResponse(cmd.Context(), id)
			if err != nil {
				return remoteFailure("get notification channel", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			return renderChannel(runtime, response.JSON200)
		},
	}
}

func newNotificationChannelCreateCommand(runtime Runtime) *cobra.Command {
	var file, key string
	command := &cobra.Command{
		Use: "create", Short: "Create a notification channel from a generated-SDK request", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if file == "" {
				return problem.Usage("--file is required")
			}
			var body sdk.CreateNotificationChannelJSONRequestBody
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
			response, err := client.CreateNotificationChannelWithResponse(cmd.Context(), &sdk.CreateNotificationChannelParams{IdempotencyKey: &resolved}, body, editors...)
			if err != nil {
				return remoteFailure("create notification channel", err)
			}
			if response.StatusCode() != http.StatusCreated || response.JSON201 == nil {
				return responseProblem(response)
			}
			return renderChannel(runtime, response.JSON201)
		},
	}
	addFileMutationFlags(command, &file, &key)
	return command
}

func newNotificationChannelUpdateCommand(runtime Runtime) *cobra.Command {
	var file, key string
	command := &cobra.Command{
		Use: "update ID", Short: "Replace a notification channel from a generated-SDK request", Args: exactArgs(1, "notification channel update requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" {
				return problem.Usage("--file is required")
			}
			id, err := parseUUID("channel ID", args[0])
			if err != nil {
				return err
			}
			var body sdk.UpdateNotificationChannelJSONRequestBody
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
			response, err := client.UpdateNotificationChannelWithResponse(cmd.Context(), id, &sdk.UpdateNotificationChannelParams{IdempotencyKey: &resolved}, body, editors...)
			if err != nil {
				return remoteFailure("update notification channel", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			return renderChannel(runtime, response.JSON200)
		},
	}
	addFileMutationFlags(command, &file, &key)
	return command
}

func newNotificationChannelDisableCommand(runtime Runtime) *cobra.Command {
	return notificationDisableCommand(runtime, "channel", func(client *sdk.ClientWithResponses, cmd *cobra.Command, id sdk.NotificationChannelID, editors []sdk.RequestEditorFn) (sdkResponse, error) {
		return client.DisableNotificationChannelWithResponse(cmd.Context(), id, editors...)
	})
}

func renderChannel(runtime Runtime, item *sdk.NotificationChannel) error {
	return renderRemote(runtime, item, output.Table{Headers: []string{"ID", "NAME", "KIND", "ENABLED"}, Rows: [][]string{{item.Id.String(), item.Name, string(item.Kind), boolValue(item.Enabled)}}})
}

func newNotificationChannelListCommand(runtime Runtime) *cobra.Command {
	return notificationListCommand(runtime, "list", "List notification channels", func(client *sdk.ClientWithResponses, cmd *cobra.Command, limit *sdk.Limit, cursor *sdk.Cursor) (any, output.Table, error) {
		response, err := client.ListNotificationChannelsWithResponse(cmd.Context(), &sdk.ListNotificationChannelsParams{Limit: limit, Cursor: cursor})
		if err != nil {
			return nil, output.Table{}, remoteFailure("list notification channels", err)
		}
		if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
			return nil, output.Table{}, responseProblem(response)
		}
		page := response.JSON200
		next := cursorValue(page.Page.NextCursor)
		rows := make([][]string, 0, len(page.Items))
		for _, item := range page.Items {
			rows = append(rows, []string{item.Id.String(), item.Name, string(item.Kind), boolValue(item.Enabled), next})
		}
		return page, output.Table{Headers: []string{"ID", "NAME", "KIND", "ENABLED", "NEXT CURSOR"}, Rows: rows}, nil
	})
}

func newNotificationRouteListCommand(runtime Runtime) *cobra.Command {
	return notificationListCommand(runtime, "list", "List notification routes", func(client *sdk.ClientWithResponses, cmd *cobra.Command, limit *sdk.Limit, cursor *sdk.Cursor) (any, output.Table, error) {
		response, err := client.ListNotificationRoutesWithResponse(cmd.Context(), &sdk.ListNotificationRoutesParams{Limit: limit, Cursor: cursor})
		if err != nil {
			return nil, output.Table{}, remoteFailure("list notification routes", err)
		}
		if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
			return nil, output.Table{}, responseProblem(response)
		}
		page := response.JSON200
		next := cursorValue(page.Page.NextCursor)
		rows := make([][]string, 0, len(page.Items))
		for _, item := range page.Items {
			rows = append(rows, []string{item.Id.String(), item.Name, item.ChannelId.String(), boolValue(item.Enabled), int32Value(item.Precedence), next})
		}
		return page, output.Table{Headers: []string{"ID", "NAME", "CHANNEL ID", "ENABLED", "PRECEDENCE", "NEXT CURSOR"}, Rows: rows}, nil
	})
}

func newNotificationRouteGetCommand(runtime Runtime) *cobra.Command {
	return &cobra.Command{
		Use: "get ID", Short: "Get a notification route", Args: exactArgs(1, "notification route get requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("route ID", args[0])
			if err != nil {
				return err
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.GetNotificationRouteWithResponse(cmd.Context(), id)
			if err != nil {
				return remoteFailure("get notification route", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			return renderRoute(runtime, response.JSON200)
		},
	}
}

func newNotificationRouteCreateCommand(runtime Runtime) *cobra.Command {
	var file, key string
	command := &cobra.Command{
		Use: "create", Short: "Create a notification route from a generated-SDK request", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if file == "" {
				return problem.Usage("--file is required")
			}
			var body sdk.CreateNotificationRouteJSONRequestBody
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
			response, err := client.CreateNotificationRouteWithResponse(cmd.Context(), &sdk.CreateNotificationRouteParams{IdempotencyKey: &resolved}, body, editors...)
			if err != nil {
				return remoteFailure("create notification route", err)
			}
			if response.StatusCode() != http.StatusCreated || response.JSON201 == nil {
				return responseProblem(response)
			}
			return renderRoute(runtime, response.JSON201)
		},
	}
	addFileMutationFlags(command, &file, &key)
	return command
}

func newNotificationRouteUpdateCommand(runtime Runtime) *cobra.Command {
	var file, key string
	command := &cobra.Command{
		Use: "update ID", Short: "Replace a notification route from a generated-SDK request", Args: exactArgs(1, "notification route update requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" {
				return problem.Usage("--file is required")
			}
			id, err := parseUUID("route ID", args[0])
			if err != nil {
				return err
			}
			var body sdk.UpdateNotificationRouteJSONRequestBody
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
			response, err := client.UpdateNotificationRouteWithResponse(cmd.Context(), id, &sdk.UpdateNotificationRouteParams{IdempotencyKey: &resolved}, body, editors...)
			if err != nil {
				return remoteFailure("update notification route", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			return renderRoute(runtime, response.JSON200)
		},
	}
	addFileMutationFlags(command, &file, &key)
	return command
}

func newNotificationRouteDisableCommand(runtime Runtime) *cobra.Command {
	return notificationDisableCommand(runtime, "route", func(client *sdk.ClientWithResponses, cmd *cobra.Command, id sdk.NotificationChannelID, editors []sdk.RequestEditorFn) (sdkResponse, error) {
		return client.DisableNotificationRouteWithResponse(cmd.Context(), id, editors...)
	})
}

func renderRoute(runtime Runtime, item *sdk.NotificationRoute) error {
	return renderRemote(runtime, item, output.Table{Headers: []string{"ID", "NAME", "CHANNEL ID", "ENABLED", "PRECEDENCE"}, Rows: [][]string{{item.Id.String(), item.Name, item.ChannelId.String(), boolValue(item.Enabled), int32Value(item.Precedence)}}})
}

func newNotificationDeliveryListCommand(runtime Runtime) *cobra.Command {
	return notificationListCommand(runtime, "list", "List notification deliveries", func(client *sdk.ClientWithResponses, cmd *cobra.Command, limit *sdk.Limit, cursor *sdk.Cursor) (any, output.Table, error) {
		response, err := client.ListNotificationDeliveriesWithResponse(cmd.Context(), &sdk.ListNotificationDeliveriesParams{Limit: limit, Cursor: cursor})
		if err != nil {
			return nil, output.Table{}, remoteFailure("list notification deliveries", err)
		}
		if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
			return nil, output.Table{}, responseProblem(response)
		}
		page := response.JSON200
		next := cursorValue(page.Page.NextCursor)
		rows := make([][]string, 0, len(page.Items))
		for _, item := range page.Items {
			rows = append(rows, []string{item.Id.String(), string(item.State), item.ChannelId.String(), item.RouteId.String(), int32Value(item.AttemptCount), next})
		}
		return page, output.Table{Headers: []string{"ID", "STATE", "CHANNEL ID", "ROUTE ID", "ATTEMPTS", "NEXT CURSOR"}, Rows: rows}, nil
	})
}

func newNotificationDeliveryGetCommand(runtime Runtime) *cobra.Command {
	return &cobra.Command{
		Use: "get ID", Short: "Get a notification delivery and bounded attempts", Args: exactArgs(1, "notification delivery get requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("delivery ID", args[0])
			if err != nil {
				return err
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			response, err := client.GetNotificationDeliveryWithResponse(cmd.Context(), id)
			if err != nil {
				return remoteFailure("get notification delivery", err)
			}
			if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
				return responseProblem(response)
			}
			detail := response.JSON200
			return renderRemote(runtime, detail, output.Table{Headers: []string{"ID", "STATE", "CHANNEL ID", "ROUTE ID", "ATTEMPTS"}, Rows: [][]string{{detail.Delivery.Id.String(), string(detail.Delivery.State), detail.Delivery.ChannelId.String(), detail.Delivery.RouteId.String(), intValue(len(detail.Attempts))}}})
		},
	}
}

func newNotificationDeliveryReplayCommand(runtime Runtime) *cobra.Command {
	var key string
	command := &cobra.Command{
		Use: "replay ID", Short: "Replay a permanently failed delivery", Args: exactArgs(1, "notification delivery replay requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID("delivery ID", args[0])
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
			response, err := client.ReplayNotificationDeliveryWithResponse(cmd.Context(), id, &sdk.ReplayNotificationDeliveryParams{IdempotencyKey: &resolved}, editors...)
			if err != nil {
				return remoteFailure("replay notification delivery", err)
			}
			if response.StatusCode() != http.StatusAccepted {
				return responseProblem(response)
			}
			return renderAction(runtime, "notification-delivery", id.String(), "queued")
		},
	}
	addMutationFlag(command, &key)
	return command
}

type notificationDisableOperation func(*sdk.ClientWithResponses, *cobra.Command, sdk.NotificationChannelID, []sdk.RequestEditorFn) (sdkResponse, error)

func notificationDisableCommand(runtime Runtime, resource string, operation notificationDisableOperation) *cobra.Command {
	var key string
	command := &cobra.Command{
		Use: "disable ID", Short: "Disable a notification " + resource, Args: exactArgs(1, "notification "+resource+" disable requires one ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUID(resource+" ID", args[0])
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
			response, err := operation(client, cmd, id, editors)
			if err != nil {
				return remoteFailure("disable notification "+resource, err)
			}
			if response.StatusCode() != http.StatusNoContent {
				return responseProblem(response)
			}
			return renderAction(runtime, "notification-"+resource, id.String(), "disabled")
		},
	}
	addMutationFlag(command, &key)
	return command
}

type notificationListOperation func(*sdk.ClientWithResponses, *cobra.Command, *sdk.Limit, *sdk.Cursor) (any, output.Table, error)

func notificationListCommand(runtime Runtime, use, short string, operation notificationListOperation) *cobra.Command {
	var limit int32
	var cursor string
	command := &cobra.Command{
		Use: use, Short: short, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			typedLimit, typedCursor, err := pageParams(limit, cursor)
			if err != nil {
				return err
			}
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return localFailure("open authenticated profile", err)
			}
			value, table, err := operation(client, cmd, typedLimit, typedCursor)
			if err != nil {
				return err
			}
			return renderRemote(runtime, value, table)
		},
	}
	command.Flags().Int32Var(&limit, "limit", 50, "maximum records (1-100)")
	command.Flags().StringVar(&cursor, "cursor", "", "opaque cursor from the previous page")
	return command
}
