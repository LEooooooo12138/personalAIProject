package gateway

import "testing"

func TestConsoleControlRoutesRequireServiceAndIdentity(t *testing.T) {
	s := consoleTestServer(t, testConsoleOrigin)
	consoleBootstrap(t, s)
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	for _, route := range []string{"/control/proposals", "/control/proposals/example/confirm", "/control/proposals/example/cancel", "/control/proposals/example/reconcile"} {
		consoleJSON(t, consoleRequest(s, "POST", consolePrefix+route, `{}`, nil, "", testConsoleOrigin, ""), 401)
		consoleJSON(t, consoleRequest(s, "POST", consolePrefix+route, `{}`, admin, csrf, testConsoleOrigin, ""), 503)
	}
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/control/proposals/example", "", admin, "", "", ""), 503)
}
