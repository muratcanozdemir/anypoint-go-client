package anypoint

import "strings"

// HostURL turns a host setting into a base URL. The region shortcuts are
// us (the default, also for ""), eu, gov, ca and jp; anything else is taken
// as a hostname or URL, with https:// assumed.
func HostURL(h string) string {
	switch strings.ToLower(h) {
	case "", "us":
		return "https://anypoint.mulesoft.com"
	case "eu":
		return "https://eu1.anypoint.mulesoft.com"
	case "gov":
		return "https://gov.anypoint.mulesoft.com"
	case "ca":
		return "https://ca1.platform.mulesoft.com"
	case "jp":
		return "https://jp1.platform.mulesoft.com"
	}
	if !strings.Contains(h, "://") {
		h = "https://" + h
	}
	return strings.TrimRight(h, "/")
}
