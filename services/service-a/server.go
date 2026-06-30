package main

import (
	"encoding/base64"
	"encoding/json"
	"html"
	"log"
	"strings"

	"github.com/gofiber/fiber/v3"
)

type userinfo struct {
	PreferredUsername string `json:"preferred_username"`
	RealmAccess       struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
}

const requiredRole = "tbox-user"

const (
	localIdentity    = "identity"
	localIDToken     = "id_token"
	localAccessToken = "access_token"
)

func main() {
	app := fiber.New(fiber.Config{
		ReadBufferSize: 1 << 20,
	})

	app.Get("/", extractIdentity, requireRole, handler)

	log.Fatal(app.Listen(":3000"))
}

func extractIdentity(c fiber.Ctx) error {
	raw := c.Get("X-Userinfo")
	if raw == "" {
		return c.Status(fiber.StatusUnauthorized).SendString("missing identity header")
	}
	// APISIX OIDC plugin base64-encodes X-Userinfo before forwarding upstream.
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).SendString("invalid identity header")
	}
	var ui userinfo
	if err := json.Unmarshal(decoded, &ui); err != nil {
		return c.Status(fiber.StatusUnauthorized).SendString("invalid identity header")
	}
	c.Locals(localIdentity, ui)
	c.Locals(localIDToken, c.Get("X-ID-Token"))
	c.Locals(localAccessToken, c.Get("X-Access-Token"))
	return c.Next()
}

func requireRole(c fiber.Ctx) error {
	ui, ok := c.Locals(localIdentity).(userinfo)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).SendString("identity not extracted")
	}
	for _, r := range ui.RealmAccess.Roles {
		if r == requiredRole {
			return c.Next()
		}
	}
	return c.Status(fiber.StatusForbidden).SendString("forbidden: missing role")
}

func handler(c fiber.Ctx) error {
	ui := c.Locals(localIdentity).(userinfo)
	idToken := c.Locals(localIDToken).(string)
	accessToken := c.Locals(localAccessToken).(string)

	name := ui.PreferredUsername
	if name == "" {
		name = "user"
	}

	uiJSON, _ := json.MarshalIndent(ui, "", "  ")

	var b strings.Builder
	b.WriteString("<h1>Hello, ")
	b.WriteString(html.EscapeString(name))
	b.WriteString("! (role: tbox-user)</h1>\n")
	b.WriteString("<h3>X-Userinfo</h3>\n<pre>")
	b.WriteString(html.EscapeString(string(uiJSON)))
	b.WriteString("</pre>\n")
	b.WriteString("<h3>X-ID-Token</h3>\n<div style=\"max-width: 50%;\">")
	b.WriteString(html.EscapeString(idToken))
	b.WriteString("</div>\n")
	b.WriteString("<h3>X-Access-Token</h3>\n<div style=\"max-width: 50%;\">")
	b.WriteString(html.EscapeString(accessToken))
	b.WriteString("</div>\n")

	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(b.String())
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s + "..."
}
