// TiCS: disabled // Header files are not built by default.

/* -*- Mode: C; tab-width: 8; indent-tabs-mode: nil; c-basic-offset: 8 -*-
 *
 * Based on https://gitlab.gnome.org/GNOME/gdm/-/blob/8c3fbd5ddefb8097d990a12581453582ec71a32f/pam-extensions/gdm-custom-json-pam-extension.h
 *
 * Copyright (C) 2023 Canonical Ltd.
 *
 * This program is free software; you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation; either version 3 of the License, or
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

#include "gdm-pam-extensions-common.h"

typedef struct {
        GdmPamExtensionMessage header;

        const char protocol_name[64];
        unsigned int version;
        char *json;
} GdmPamExtensionJSONProtocol;

#define GDM_PAM_EXTENSION_CUSTOM_JSON "org.gnome.DisplayManager.UserVerifier.CustomJSON"
#define GDM_PAM_EXTENSION_CUSTOM_JSON_SIZE sizeof (GdmPamExtensionJSONProtocol)

#define GDM_PAM_EXTENSION_CUSTOM_JSON_REQUEST_INIT(request, proto_name, proto_version, json_str) \
{ \
        size_t proto_len = strnlen ((proto_name), sizeof ((request)->protocol_name) - 1); \
        GDM_PAM_EXTENSION_LOOK_UP_TYPE (GDM_PAM_EXTENSION_CUSTOM_JSON, &((request)->header.type)); \
        (request)->header.length = htobe32 (GDM_PAM_EXTENSION_CUSTOM_JSON_SIZE); \
        memcpy ((char *)(request)->protocol_name, (proto_name), proto_len); \
        ((char *)((request)->protocol_name))[proto_len] = '\0'; \
        (request)->version = (proto_version); \
        (request)->json = (char *) (json_str); \
}

#define GDM_PAM_EXTENSION_CUSTOM_JSON_RESPONSE_INIT(response, proto_name, proto_version) \
{ \
        size_t proto_len = strnlen ((proto_name), sizeof ((response)->protocol_name) - 1); \
        GDM_PAM_EXTENSION_LOOK_UP_TYPE (GDM_PAM_EXTENSION_CUSTOM_JSON, &((response)->header.type)); \
        (response)->header.length = htobe32 (GDM_PAM_EXTENSION_CUSTOM_JSON_SIZE); \
        memcpy ((char *)(response)->protocol_name, (proto_name), proto_len); \
        ((char *)((response)->protocol_name))[proto_len] = '\0'; \
        (response)->version = (proto_version); \
        (response)->json = NULL; \
}

#define GDM_PAM_EXTENSION_REPLY_TO_CUSTOM_JSON_RESPONSE(reply) \
        ((GdmPamExtensionJSONProtocol *) (void *) reply->resp)
