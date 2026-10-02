package billing

import (
	"fmt"
	"net/url"
	"strings"
)

// GroupScope encodes each name independently so slashes and percent signs cannot
// alias a different tenant/group pair. Tenant and key scopes remain unchanged.
func GroupScope(tenant, group string) string {
	return "group:" + url.PathEscape(tenant) + "/" + url.PathEscape(group)
}

func ParseGroupScope(scope string) (tenant, group string, err error) {
	parts := strings.Split(strings.TrimPrefix(scope, "group:"), "/")
	if !strings.HasPrefix(scope, "group:") || len(parts) != 2 {
		return "", "", fmt.Errorf("%w: ambiguous group quota scope", ErrInvalidPolicy)
	}
	tenant, err = url.PathUnescape(parts[0])
	if err != nil {
		return "", "", fmt.Errorf("%w: invalid tenant escape", ErrInvalidPolicy)
	}
	group, err = url.PathUnescape(parts[1])
	if err != nil || tenant == "" || group == "" || GroupScope(tenant, group) != scope {
		return "", "", fmt.Errorf("%w: noncanonical group quota scope", ErrInvalidPolicy)
	}
	return tenant, group, nil
}
