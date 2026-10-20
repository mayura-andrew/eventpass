// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var hexColorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ValidColor reports whether s is a #RRGGBB color.
func ValidColor(s string) bool { return hexColorRE.MatchString(s) }

func luminance(hex string) float64 {
	ch := func(i int) float64 {
		v, _ := strconv.ParseUint(hex[i:i+2], 16, 8)
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(1) + 0.7152*ch(3) + 0.0722*ch(5)
}

// ContrastRatio is the WCAG 2.1 contrast ratio between two #RRGGBB colors.
func ContrastRatio(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// WCAGAAText is the minimum contrast for normal text.
const WCAGAAText = 4.5

// WorkspaceSettingKeys are the keys the Workspace tab accepts.
var WorkspaceSettingKeys = []string{
	"product_name", "logo_blob_id", "favicon_blob_id", "primary_color", "accent_color",
	"default_locale", "time_zone", "support_email", "footer_text", "privacy_url", "terms_url",
}

// ValidateWorkspaceSettings checks values an owner is saving. It returns
// field errors and non-blocking warnings (such as low contrast).
func ValidateWorkspaceSettings(v map[string]string) (errs map[string]string, warnings []string) {
	errs = map[string]string{}
	allowed := map[string]bool{}
	for _, k := range WorkspaceSettingKeys {
		allowed[k] = true
	}
	for k, val := range v {
		switch {
		case !allowed[k]:
			errs[k] = "unknown_setting"
		case len(val) > 2000:
			errs[k] = "too_long"
		case (k == "primary_color" || k == "accent_color") && val != "" && !ValidColor(val):
			errs[k] = "invalid_color"
		case (k == "privacy_url" || k == "terms_url") && val != "" && !validHTTPURL(val):
			errs[k] = ErrInvalidURL
		case k == "support_email" && val != "":
			if _, e := normalizeOne(Field{Type: FieldEmail}, val); e != nil {
				errs[k] = ErrInvalidEmail
			}
		case k == "time_zone" && val != "":
			if _, err := time.LoadLocation(val); err != nil {
				errs[k] = "invalid_time_zone"
			}
		case k == "product_name" && strings.TrimSpace(val) == "":
			errs[k] = ErrRequired
		}
	}
	if c := v["primary_color"]; ValidColor(c) && ContrastRatio(c, "#FFFFFF") < WCAGAAText {
		warnings = append(warnings, "primary_color_low_contrast")
	}
	return errs, warnings
}

func validHTTPURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}
