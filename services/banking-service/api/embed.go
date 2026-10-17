package api

import _ "embed"

//go:embed openapi.yaml
var OpenAPI []byte

//go:embed coze-plugin.openapi.yaml
var PluginOpenAPI []byte
