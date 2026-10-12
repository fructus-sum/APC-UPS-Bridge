// Package firmware checks reported UPS firmware against a small, reviewed
// manufacturer reference. It never downloads, uploads or flashes firmware.
package firmware

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/apc"
)

// Source: Schneider Electric FA279197, reviewed 2026-10-10.
// The reference is intentionally conservative and incomplete. Entries are keyed
// by BOTH family and UPS ID; the same UPS ID may mean different firmware in
// different product families. Update these entries only after reviewing official
// Schneider release notes and special-case exclusions.
const ReferenceDate = "2026-10-10"
const GuidanceURL = "https://www.se.com/us/en/faqs/FA279197/"

var latest = map[string]string{
	"SMC/1000":  "10.0",
	"SMC/1005":  "15.1",
	"SMC/1007":  "15.0",
	"SMC/1018":  "18.2",
	"SMC/1028":  "18.2",
	"SMT/17":    "07.1",
	"SMT/18":    "15.1",
	"SMT/1015":  "18.0",
	"SMT/1027":  "18.0",
	"SMT/1031":  "15.6",
	"SMT/1039":  "15.5",
	"SMT/1040":  "15.0",
	"SMT/1041":  "15.0",
	"SMT/1050":  "16.6",
	"SMT/1059":  "16.5",
	"SMX/1003":  "15.0",
	"SMX/1031":  "15.6",
	"SMX/1050":  "16.6",
	"SMX/20":    "15.0",
	"SMX/23":    "15.0",
	"SCL/1029":  "15.6",
	"SCL/1030":  "15.6",
	"SCL/1036":  "15.6",
	"SCL/1037":  "15.6",
	"SMTL/1026": "15.5",
	"SMTL/1047": "16.5",
}

var (
	upsFirmware = regexp.MustCompile("(?i)\\bUPS\\s+([0-9]{1,2}\\.[0-9]{1,2})\\s*/\\s*ID\\s*=\\s*([0-9]{1,5})\\b")
	familySKU   = regexp.MustCompile("(?i)\\b(SMTL|SMT|SMX|SMC|SCL|SRTL|SRT|CSH|XP|XU)([0-9][A-Z0-9-]*)\\b")
	legacySKU   = regexp.MustCompile("(?i)\\b(SURTD|SURTA|SURT|SUA|SUM|SU)[0-9][A-Z0-9-]*\\b")
)

// Result is shown only in the authenticated web UI. A status of "outdated"
// requires a verified family, UPS ID, and parseable firmware version.
type Result struct {
	Status        string `json:"status"`
	Installed     string `json:"installed,omitempty"`
	Latest        string `json:"latest,omitempty"`
	Family        string `json:"family,omitempty"`
	UPSID         string `json:"ups_id,omitempty"`
	ReferenceDate string `json:"reference_date,omitempty"`
	GuidanceURL   string `json:"guidance_url"`
	Message       string `json:"message"`
}

func Check(s *apc.Snapshot) Result {
	r := Result{Status: "unknown", GuidanceURL: GuidanceURL, Message: "Firmware update status could not be verified for this UPS. Check Schneider Electric's official instructions."}
	if s == nil {
		r.Status = "unavailable"
		r.Message = "UPS communication is unavailable; firmware cannot be checked."
		return r
	}
	r.Installed = strings.TrimSpace(s.Firmware)
	modelID := strings.TrimSpace(s.Model + " " + s.SKU)
	if legacySKU.MatchString(modelID) {
		r.Status = "not_upgradeable"
		r.Message = "Schneider Electric lists this older Smart-UPS family as not user-upgradeable."
		return r
	}
	// A generic name like "Smart-UPS 2200" is NOT sufficient to identify
	// an SMT/SMX/etc. series. Do not infer firmware compatibility from it.
	m := familySKU.FindStringSubmatch(modelID)
	if len(m) == 0 {
		return r
	}
	r.Family = strings.ToUpper(m[1])
	// Explicit exceptions from Schneider's Firmware Upgrade Wizard guidance.
	sku := strings.ToUpper(m[0])
	if sku == "SMT2200" || sku == "SMT750RMJ1U" || sku == "SMC2200XLI-BR" {
		return r
	}
	f := upsFirmware.FindStringSubmatch(r.Installed)
	if len(f) == 0 {
		return r
	}
	r.UPSID = f[2]
	if n, err := strconv.Atoi(r.UPSID); err == nil {
		r.UPSID = strconv.Itoa(n)
	}
	key := r.Family + "/" + r.UPSID
	reference, exists := latest[key]
	if !exists {
		return r
	}
	// Some SMX750/1000/1500 with UPS ID 11 and firmware 02.x are not
	// upgradeable; ID 11 is deliberately absent from our vetted reference.
	current, ok1 := versionParts(f[1])
	target, ok2 := versionParts(reference)
	if !ok1 || !ok2 {
		return r
	}
	r.ReferenceDate = ReferenceDate
	r.Latest = reference
	switch {
	case current[0] < target[0] || (current[0] == target[0] && current[1] < target[1]):
		r.Status = "outdated"
		r.Message = "A newer firmware version is listed by Schneider Electric. Follow the manufacturer's manual update procedure; a firmware update can temporarily remove battery backup protection."
	case current == target:
		r.Status = "current"
		r.Message = "Installed firmware matches the reviewed Schneider Electric reference. This is not a live update check."
	default:
		r.Status = "unknown"
		r.Message = "Installed firmware is newer than the local reference; consult Schneider Electric before taking action."
		r.Latest = ""
	}
	return r
}

func versionParts(value string) ([2]int, bool) {
	var result [2]int
	a, b, ok := strings.Cut(value, ".")
	if !ok {
		return result, false
	}
	var err error
	result[0], err = strconv.Atoi(a)
	if err != nil {
		return result, false
	}
	result[1], err = strconv.Atoi(b)
	return result, err == nil
}
