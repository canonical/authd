// TiCS: disabled // Header files are not built by default.

#include <stdlib.h>
#include <string.h>

// FIXME: Use system one once possible.
#include "extensions/gdm-custom-json-pam-extension.h"

#define JSON_PROTO_NAME ((const char *) "com.ubuntu.authd.gdm")
#define JSON_PROTO_VERSION 1U

static char pam_extension_environment_block[_POSIX_ARG_MAX];
static char **supported_extensions = NULL;

static inline bool
is_gdm_pam_extension_supported (const char *extension)
{
  return GDM_PAM_EXTENSION_SUPPORTED (extension);
}

static inline void
gdm_extensions_advertise_supported (const char *extensions[],
                                    size_t      n_extensions)
{
  if (supported_extensions)
    {
      for (size_t i = 0; supported_extensions[i] != NULL; ++i)
        free (supported_extensions[i]);

      free (supported_extensions);
    }

  supported_extensions = malloc ((n_extensions + 1) * sizeof (char *));

  for (size_t i = 0; i < n_extensions; ++i)
    supported_extensions[i] = strdup (extensions[i]);
  supported_extensions[n_extensions] = NULL;

  GDM_PAM_EXTENSION_ADVERTISE_SUPPORTED_EXTENSIONS (
    pam_extension_environment_block, supported_extensions);
}

static inline void
gdm_custom_json_request_init (GdmPamExtensionJSONProtocol *request,
                              const char                  *proto_name,
                              unsigned int                 proto_version,
                              const char                  *json)
{
  GDM_PAM_EXTENSION_CUSTOM_JSON_REQUEST_INIT (request, proto_name,
                                              proto_version, json);
}

static inline void
gdm_custom_json_request_init_authd (GdmPamExtensionJSONProtocol *request,
                                    const char                  *json)
{
  GDM_PAM_EXTENSION_CUSTOM_JSON_REQUEST_INIT (request, JSON_PROTO_NAME,
                                              JSON_PROTO_VERSION, json);
}

static inline bool
gdm_custom_json_request_is_valid_authd (GdmPamExtensionJSONProtocol *request)
{
  if (request->version != JSON_PROTO_VERSION)
    return false;

  return strncmp (request->protocol_name, JSON_PROTO_NAME, sizeof (request->protocol_name)) == 0;
}
