package sysinfo

import (
	"strings"
	"unicode"
)

// jedecMaker names the memory makers whose JEDEC ID firmware often writes
// instead of a name. Anything else is shown as the firmware wrote it.
func jedecMaker(id string) (string, bool) {
	s := strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(id), "0x"))
	if len(s) > 4 && isHex(s) {
		s = s[:4]
	}
	switch s {
	case "80CE", "CE00", "00CE":
		return "Samsung", true
	case "80AD", "AD00", "00AD":
		return "SK hynix", true
	case "802C", "2C00", "002C":
		return "Micron", true
	case "859B", "9B05", "059B":
		return "Crucial", true
	case "0198", "8198", "9801":
		return "Kingston", true
	case "04CB", "84CB", "CB04":
		return "ADATA", true
	case "04CD", "84CD", "CD04":
		return "G.Skill", true
	case "029E", "829E", "9E02":
		return "Corsair", true
	}
	return "", false
}

// pnpMaker names the screen makers by the three-letter code in the EDID.
func pnpMaker(code string) string {
	switch code {
	case "AUO":
		return "AU Optronics"
	case "BOE":
		return "BOE"
	case "CMN":
		return "Innolux"
	case "LGD":
		return "LG Display"
	case "SDC":
		return "Samsung Display"
	case "SHP":
		return "Sharp"
	case "IVO":
		return "InfoVision"
	case "CSO":
		return "CSOT"
	case "DEL":
		return "Dell"
	case "HPN", "HWP":
		return "HP"
	case "SAM":
		return "Samsung"
	case "GSM":
		return "LG"
	case "ACR":
		return "Acer"
	case "AUS":
		return "ASUS"
	case "BNQ":
		return "BenQ"
	case "LEN":
		return "Lenovo"
	case "PHL":
		return "Philips"
	case "AOC":
		return "AOC"
	case "MSI":
		return "MSI"
	case "VSC":
		return "ViewSonic"
	case "APP":
		return "Apple"
	}
	return code
}

// isHex reports whether s is all hex digits.
func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789ABCDEFabcdef", r) {
			return false
		}
	}
	return true
}

// placeholders are what firmware writes when the maker never filled a field
// in. They are compared squashed (see [squash]), so "SerialNumber",
// "Serial Number" and "serial_number" are one placeholder.
func placeholderWords() []string {
	return []string{
		"to be filled by o.e.m.", "to be filled by oem", "default string", "system serial number",
		"not specified", "not applicable", "none", "n/a", "na", "unknown", "undefined", "empty",
		"system product name", "system manufacturer", "system version", "base board serial number",
		"base board manufacturer", "base board product name", "chassis serial number", "invalid",
		"0123456789", "123456789", "1234567890", "12345678", "1234567", "o.e.m.", "oem", "no dimm",
		"not available", "serial number", "serial num", "serial", "sn", "type1productconfigid", "sku",
		"default", "xxxxxxxx", "not defined", "not present", "none defined", "oem default",
		"oem default string", "system sku", "system sku number", "chassis manufacturer",
		"chassis version", "base board version", "asset tag", "battery serial number",
		"battery serial", "battery manufacturer", "battery name", "manufacturer name",
		"device name", "unique id",
	}
}

// placeholderPrefixes are numbered placeholders such as "SerNum0",
// "Manufacturer00" or "OEM_Define1", compared squashed.
func placeholderPrefixes() []string {
	return []string{"sernum", "partnum", "manufacturer", "assettagnum", "assettag", "array", "module", "oemdefine", "serialnumber"}
}

// squash lower-cases s and keeps only its letters and digits, so firmware
// spellings that differ only in spaces, dots or underscores compare equal.
func squash(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isPlaceholder reports whether a firmware string means "not set".
func isPlaceholder(s string) bool {
	t := strings.ToLower(strings.TrimSpace(s))
	if t == "" {
		return true
	}
	q := squash(t)
	if q == "" {
		return true
	}
	for _, p := range placeholderWords() {
		if q == squash(p) {
			return true
		}
	}
	for _, p := range placeholderPrefixes() {
		if rest, ok := strings.CutPrefix(q, p); ok && rest != "" && strings.Trim(rest, "0123456789") == "" {
			return true
		}
	}
	// One character over and over: "00000000", "FFFFFFFF", "........".
	if len(t) >= 4 && strings.Count(t, t[:1]) == len(t) {
		return true
	}
	return false
}

// clean returns s trimmed, or "" when it is a placeholder.
func clean(s string) string {
	s = strings.TrimSpace(strings.Trim(s, "\x00"))
	if isPlaceholder(s) {
		return ""
	}
	return s
}

// brandedMakers are the PC makers whose machines leave the factory with a
// real serial number. A placeholder serial on one of them is worth asking
// about.
func brandedMaker(maker string) bool {
	m := strings.ToLower(maker)
	for _, b := range []string{
		"dell", "hp", "hewlett", "lenovo", "asus", "acer", "microsoft", "apple", "samsung",
		"msi", "micro-star", "razer", "toshiba", "dynabook", "fujitsu", "lg electronics", "huawei", "xiaomi", "framework",
	} {
		if strings.Contains(m, b) {
			return true
		}
	}
	return false
}
