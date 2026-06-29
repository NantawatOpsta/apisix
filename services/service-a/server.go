package main

import (
	"encoding/base64"
	"encoding/json"
	"log"

	"github.com/gofiber/fiber/v3"
)

type userinfo struct {
	PreferredUsername string `json:"preferred_username"`
	RealmAccess       struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
}

const requiredRole = "tbox-user"

func main() {
	app := fiber.New(fiber.Config{
		ReadBufferSize: 1 << 20,
	})

	app.Use(requireIdentity)

	app.Get("/", func(c fiber.Ctx) error {
		ui, _ := c.Locals("userinfo").(userinfo)
		name := ui.PreferredUsername
		if name == "" {
			name = "user"
		}
		return c.SendString("Hello, " + name + "! (role: " + requiredRole + ")")
	})

	log.Fatal(app.Listen(":3000"))
}

func requireIdentity(c fiber.Ctx) error {
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
	for _, r := range ui.RealmAccess.Roles {
		if r == requiredRole {
			c.Locals("userinfo", ui)
			return c.Next()
		}
	}
	return c.Status(fiber.StatusForbidden).SendString("forbidden: missing role")
}