package i18n

func init() {
	reg("en", map[string]string{
		"app.name":      "Iris",
		"local.bundled": "Bundled Iris client found", "local.system": "System Iris client found",
		"local.none": "No local Iris client detected.",
	})
}
