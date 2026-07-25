package main

import (
	"bytes"
	"context"
	"flag"
	"strings"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
)

func TestNotificationWorkerFlagsExposeValidatedOperationalBounds(t *testing.T) {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	flags.SetOutput(&bytes.Buffer{})
	values := addNotificationWorkerFlags(flags)
	if err := flags.Parse([]string{
		"--notification-batch-size", "7",
		"--notification-concurrency", "2",
		"--notification-lease", "30s",
		"--notification-poll", "250ms",
		"--notification-send-timeout", "5s",
		"--notification-max-attempts", "3",
		"--notification-backoff-base", "2s",
		"--notification-backoff-cap", "1m",
		"--notification-egress-allow-cidrs", "192.168.1.0/24, 100.64.0.0/10",
		"--notification-egress-deny-cidrs", "10.96.0.0/12",
	}); err != nil {
		t.Fatal(err)
	}
	if values.batchSize != 7 || values.concurrency != 2 || values.leaseDuration != 30*time.Second || values.maxAttempts != 3 {
		t.Fatalf("worker flags = %#v", values)
	}
	if got := splitCIDRs(values.allowedCIDRs); len(got) != 2 || got[1] != "100.64.0.0/10" {
		t.Fatalf("allowed CIDRs = %#v", got)
	}
}

func TestNotificationWorkerBuildSkipsWithoutKeyringAndRejectsUnsafeBounds(t *testing.T) {
	values := &notificationWorkerFlagValues{
		batchSize: 20, concurrency: 4, leaseDuration: 45 * time.Second,
		pollInterval: time.Second, sendTimeout: 15 * time.Second,
		maxAttempts: 8, backoffBase: 5 * time.Second, backoffCap: 15 * time.Minute,
	}
	worker, err := values.build(nil, nil, nil, nil, "", nil)
	if err != nil || worker != nil {
		t.Fatalf("build without keyring = %#v, %v", worker, err)
	}
	values = &notificationWorkerFlagValues{
		batchSize: 1, concurrency: 1, leaseDuration: time.Second,
		pollInterval: time.Second, sendTimeout: 2 * time.Second,
		maxAttempts: 1, backoffBase: time.Second, backoffCap: time.Second,
	}
	_, err = values.build(stubUnitOfWork{}, plainCommandSealer{}, stubTokenIssuer{}, func() string { return "id" }, "owner", nil)
	if err == nil || !strings.Contains(err.Error(), "operational bounds") {
		t.Fatalf("unsafe bounds error = %v", err)
	}
}

type stubUnitOfWork struct{}

func (stubUnitOfWork) View(context.Context, func(context.Context, port.Repositories) error) error {
	return nil
}
func (stubUnitOfWork) Transact(context.Context, func(context.Context, port.Repositories) error) error {
	return nil
}

type plainCommandSealer struct{}

func (plainCommandSealer) ActiveVersion() uint32 { return 1 }
func (plainCommandSealer) CanOpen(uint32) bool   { return true }
func (plainCommandSealer) Seal(context.Context, port.ConfigIdentity, []byte) (port.SealedConfig, error) {
	return port.SealedConfig{KeyVersion: 1}, nil
}
func (plainCommandSealer) Open(context.Context, port.ConfigIdentity, port.SealedConfig) ([]byte, error) {
	return nil, nil
}

type stubTokenIssuer struct{}

func (stubTokenIssuer) New() (application.IssuedToken, error) { panic("unused") }
func (stubTokenIssuer) Hash(string) []byte                    { panic("unused") }
