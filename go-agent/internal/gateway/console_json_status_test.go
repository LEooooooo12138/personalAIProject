package gateway

import "testing"

func TestConsoleJSONSyntaxStatus(t *testing.T) {
	s := consoleTestServer(t, testConsoleOrigin)
	consoleBootstrap(t, s)
	cookie, csrf, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"empty", "", 400},
		{"whitespace", " \n\t", 400},
		{"syntax", `{"username":}`, 400},
		{"incomplete", `{"username":"new"`, 400},
		{"trailing_document", `{"username":"new","display_name":"N"} {}`, 400},
		{"trailing_garbage", `{"username":"new","display_name":"N"} x`, 400},
		{"unknown_field", `{"username":"new","display_name":"N","role":"admin"}`, 422},
		{"field_type", `{"username":42,"display_name":"N"}`, 422},
		{"missing_fields", `{}`, 422},
		{"business_validation", `{"username":"x","display_name":"N"}`, 422},
		{"nonobject", `[]`, 422},
		{"null", `null`, 422},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := consoleRequest(s, "POST", consolePrefix+"/admin/members", tc.body, cookie, csrf, testConsoleOrigin, "")
			v := consoleJSON(t, w, tc.status)
			err := v["error"].(map[string]any)
			if err["code"] != "invalid_request" || err["request_id"] == "" {
				t.Fatalf("invalid error envelope: %v", v)
			}
		})
	}
}
