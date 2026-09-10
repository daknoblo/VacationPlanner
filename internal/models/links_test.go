package models

import "testing"

func TestItemLinksOnlyAllowSafeBrowserURLs(t *testing.T) {
	for _, raw := range []string{"javascript:alert(1)", "data:text/html,test", "https://user:password@example.com", "https://example.com/\nunsafe", "//example.com"} {
		if SafeExternalURL(raw) != "" {
			t.Errorf("unsafe link allowed: %q", raw)
		}
		if ValidateItemLinks([]ItemLink{{Kind: "website", URL: raw}}) == nil {
			t.Errorf("invalid link accepted: %q", raw)
		}
	}
	if err := ValidateItemLinks([]ItemLink{{Kind: "wikipedia", URL: "https://en.wikipedia.org/wiki/Rome"}}); err != nil {
		t.Fatal(err)
	}
	if ValidateItemLinks([]ItemLink{{Kind: "unknown", URL: "https://example.com"}}) == nil {
		t.Fatal("unknown kind accepted")
	}
	if ValidateItemLinks(make([]ItemLink, 9)) == nil {
		t.Fatal("unbounded links accepted")
	}
}
