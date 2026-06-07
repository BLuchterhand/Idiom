package app

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
)

func formatModelFailureStatus(err error) string {
	if err == nil {
		return ""
	}
	classification := "non-retryable"
	nextAction := "inspect the error before retrying"
	if isRetryableModelTransportFailure(err) {
		classification = "retryable"
		nextAction = "retry the same command; existing product artifacts were preserved"
	}
	return "Chat failed: " + err.Error() + ". Classification: " + classification + " model failure. Next safe action: " + nextAction + "."
}

func isRetryableModelTransportFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	lower := strings.ToLower(err.Error())
	for _, marker := range []string{
		"transport failed",
		"connection refused",
		"connection reset",
		"connection closed",
		"unexpected eof",
		"server not ready",
		"timeout",
		"temporarily unavailable",
		"service unavailable",
		"bad gateway",
		"gateway timeout",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
