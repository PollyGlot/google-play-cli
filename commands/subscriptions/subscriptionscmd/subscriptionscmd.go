package subscriptionscmd

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const DefaultDir = "./monetization/subscriptions"

const DefaultRegionsVersion = "2022/02"

type forbiddenError struct {
	pkg   string
	cause error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account cannot manage the subscription catalog for %q: grant it access to the app's monetization setup in Play Console (Users & permissions), then retry: %v", e.pkg, e.cause)
}
func (e *forbiddenError) Unwrap() error { return e.cause }

type notFoundError struct {
	pkg    string
	target string
	cause  error
}

func (e *notFoundError) Error() string {
	if e.target != "" {
		return fmt.Sprintf("subscription %s not found in package %q: check the ids given; if the app is wrong, verify --package (or the project pin): %v", e.target, e.pkg, e.cause)
	}
	return fmt.Sprintf("package %q not found: verify --package (or the project pin) names an app this service account can reach: %v", e.pkg, e.cause)
}
func (e *notFoundError) Unwrap() error { return e.cause }

func Classify(pkg string, err error) error {
	return ClassifyAt(pkg, "", err)
}

func ClassifyAt(pkg, target string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusForbidden:
			return &forbiddenError{pkg: pkg, cause: err}
		case http.StatusNotFound:
			return &notFoundError{pkg: pkg, target: target, cause: err}
		}
	}
	return err
}

func Target(product, basePlan, offer string) string {
	t := fmt.Sprintf("product %q", product)
	if basePlan != "" {
		t += fmt.Sprintf(" base plan %q", basePlan)
	}
	if offer != "" {
		t += fmt.Sprintf(" offer %q", offer)
	}
	return t
}
