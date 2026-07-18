package apperr

// Plugin-domain errors.
var (
	PluginNameRequired = New("plugin_name_required", StatusBadRequest, "errors.plugin.pluginNameRequired")
	PluginNotFound     = New("plugin_not_found", StatusNotFound, "errors.plugin.pluginNotFound")
	InvalidParams      = New("invalid_params", StatusBadRequest, "errors.plugin.invalidParams")
	PluginCreateFailed = New("plugin_create_failed", StatusBadRequest, "errors.plugin.createFailed")
	PluginDeleteFailed = New("plugin_delete_failed", StatusBadRequest, "errors.plugin.deleteFailed")
	PluginUpdateFailed = New("plugin_update_failed", StatusBadRequest, "errors.plugin.updateFailed")
)
