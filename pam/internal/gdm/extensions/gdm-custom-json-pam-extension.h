// TiCS: disabled // Header files are not built by default.

/* -*- Mode: C; tab-width: 8; indent-tabs-mode: nil; c-basic-offset: 8 -*-
 *
 * Based on https://gitlab.gnome.org/GNOME/gdm/-/blob/9038d1c82c504385d9a85cbdbaa7a5ab2d484520/pam-extensions/gdm-custom-json-pam-extension.h
 *
 * Copyright (C) 2023 Canonical Ltd.
 *
 * This program is free software; you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation; either version 2 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, write to the Free Software
 * Foundation, Inc., 51 Franklin Street, Fifth Floor, Boston, MA 02110-1301, USA.
 *
 * Author: Marco Trevisan (Treviño) <marco.trevisan@canonical.com>
 *
 */

#pragma once

#include <assert.h>

#include "gdm-pam-extensions-common.h"

typedef struct {
        GdmPamExtensionMessage header;

        const char protocol_name[64];
        unsigned int version;
        char *json;
} GdmPamExtensionJSONProtocol;

#define GDM_PAM_EXTENSION_CUSTOM_JSON "org.gnome.DisplayManager.UserVerifier.CustomJSON"
#define GDM_PAM_EXTENSION_CUSTOM_JSON_SIZE sizeof (GdmPamExtensionJSONProtocol)

static inline void
init_json_protocol_base (GdmPamExtensionJSONProtocol *protocol,
                         const char                  *proto_name,
                         unsigned int                 proto_version)
{
        bool type_found;
        size_t proto_len;

        type_found = gdm_pam_extension_look_up_type (GDM_PAM_EXTENSION_CUSTOM_JSON,
                                                     &protocol->header.type);
        assert (type_found);

        proto_len = strnlen (proto_name, sizeof (protocol->protocol_name) - 1);

        protocol->header.length = htobe32 (GDM_PAM_EXTENSION_CUSTOM_JSON_SIZE);
        memcpy ((char *) protocol->protocol_name, proto_name, proto_len);
        ((char *) protocol->protocol_name)[proto_len] = '\0';
        protocol->version = proto_version;
}

static inline void
gdm_pam_extension_custom_json_request_init (GdmPamExtensionJSONProtocol *request,
                                            const char                  *proto_name,
                                            unsigned int                 proto_version,
                                            const char                  *json_str)
{
        init_json_protocol_base (request, proto_name, proto_version);
        request->json = (char *) json_str;
}

static inline void
gdm_pam_extension_custom_json_response_init (GdmPamExtensionJSONProtocol *response,
                                             const char                  *proto_name,
                                             unsigned int                 proto_version)
{
        init_json_protocol_base (response, proto_name, proto_version);
        response->json = NULL;
}

static inline GdmPamExtensionJSONProtocol *
gdm_pam_extension_reply_to_custom_json_response (const struct pam_response *reply)
{
        return (GdmPamExtensionJSONProtocol *) (void *) reply->resp;
}

static inline void
gdm_pam_extension_custom_json_response_free (GdmPamExtensionJSONProtocol *response)
{
        if (response == NULL)
                return;

        if (response->json != NULL) {
                gdm_pam_extension_zero_buffer (response->json, strlen (response->json));
                free (response->json);
        }
        free (response);
}

#ifdef __G_LIB_H__
G_DEFINE_AUTOPTR_CLEANUP_FUNC (GdmPamExtensionJSONProtocol, gdm_pam_extension_custom_json_response_free)
#endif
