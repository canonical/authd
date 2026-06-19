// TiCS: disabled // Header files are not built by default.

/* -*- Mode: C; tab-width: 8; indent-tabs-mode: nil; c-basic-offset: 8 -*-
 *
 * Copyright (C) 2017 Red Hat, Inc.
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
 */
#ifndef GDM_PAM_EXTENSIONS_COMMON_H
#define GDM_PAM_EXTENSIONS_COMMON_H

#include <alloca.h>
#include <endian.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <limits.h>

#include <security/pam_appl.h>

typedef struct {
        uint32_t length;

        unsigned char type;
        unsigned char data[];
} GdmPamExtensionMessage;

#define GDM_PAM_EXTENSION_MESSAGE_FROM_PAM_MESSAGE(query) (GdmPamExtensionMessage *) (void *) query->msg
#define GDM_PAM_EXTENSION_MESSAGE_TO_PAM_REPLY(msg) (char *) (void *) msg
#define GDM_PAM_EXTENSION_MESSAGE_TO_BINARY_PROMPT_MESSAGE(extended_message, binary_message) \
{ \
        (binary_message)->msg_style = PAM_BINARY_PROMPT; \
        (binary_message)->msg = (void *) extended_message; \
}
#define GDM_PAM_EXTENSION_MESSAGE_TRUNCATED(msg) be32toh(msg->length) < sizeof (GdmPamExtensionMessage)
#define GDM_PAM_EXTENSION_MESSAGE_INVALID_TYPE(msg) \
({ \
        bool _invalid = true; \
        int _n = -1; \
        const char *_supported_extensions; \
        _supported_extensions = getenv ("GDM_SUPPORTED_PAM_EXTENSIONS"); \
        if (_supported_extensions != NULL) { \
                const char *_p = _supported_extensions; \
                while (*_p != '\0' && _n < UCHAR_MAX) { \
                        size_t _length; \
                        _length = strcspn (_p, " "); \
                        if (_length > 0) \
                                _n++; \
                        _p += _length; \
                        _length = strspn (_p, " "); \
                        _p += _length; \
                } \
                if (_n >= msg->type) \
                        _invalid = false; \
        } \
        _invalid; \
})
#define GDM_PAM_EXTENSION_MESSAGE_MATCH(msg, supported_extensions, name) (strcmp (supported_extensions[msg->type], name) == 0)

/* environment block should be a statically allocated chunk of memory.  This is important because
 * putenv() will leak otherwise (and setenv isn't thread safe)
 */
#define GDM_PAM_EXTENSION_ADVERTISE_SUPPORTED_EXTENSIONS(environment_block, supported_extensions) \
{ \
        size_t _size = 0; \
        unsigned char _t, _num_chunks; \
        char *_p; \
        _p = environment_block; \
        _p = stpncpy (_p, "GDM_SUPPORTED_PAM_EXTENSIONS", sizeof(environment_block)); \
        *_p = '\0'; \
        _size += strlen (environment_block); \
        for (_t = 0; supported_extensions[_t] != NULL && _t < UCHAR_MAX; _t++) {\
                size_t _next_chunk = strlen (supported_extensions[_t]) + strlen (" "); \
                if (_size + _next_chunk >= sizeof (environment_block)) \
                        break; \
                _size += _next_chunk; \
        }\
        _num_chunks = _t; \
        if (_t != 0) { \
                _p = stpcpy (_p, "="); \
                for (_t = 0; _t < _num_chunks; _t++) { \
                        if (_t != 0) \
                                _p = stpcpy (_p, " "); \
                        _p = stpcpy (_p, supported_extensions[_t]); \
                } \
                *_p = '\0'; \
                putenv (environment_block); \
        } \
}

#define GDM_PAM_EXTENSION_LOOK_UP_TYPE(name, extension_type) \
({ \
        bool _supported = false; \
        unsigned char _t = 0; \
        const char *_supported_extensions; \
        _supported_extensions = getenv ("GDM_SUPPORTED_PAM_EXTENSIONS"); \
        if (_supported_extensions != NULL) { \
                const char *_p = _supported_extensions; \
                while (*_p != '\0') { \
                        size_t _length; \
                        _length = strcspn (_p, " "); \
                        if (strncmp (_p, name, _length) == 0) { \
                                _supported = true; \
                                break; \
                        } \
                        _p += _length; \
                        _length = strspn (_p, " "); \
                        _p += _length; \
                        if (_t >= UCHAR_MAX) { \
                                break; \
                        } \
                        _t++; \
                } \
                if (_supported && extension_type != NULL) \
                        *extension_type = _t; \
        } \
        _supported; \
})

#define GDM_PAM_EXTENSION_SUPPORTED(name) GDM_PAM_EXTENSION_LOOK_UP_TYPE(name, (unsigned char *) NULL)

#endif
