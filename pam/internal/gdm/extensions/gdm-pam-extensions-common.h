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

#pragma once

#include <endian.h>
#include <limits.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include <security/pam_appl.h>

typedef struct {
        uint32_t length;

        unsigned char type;
        unsigned char data[];
} GdmPamExtensionMessage;

static inline void
gdm_pam_extension_zero_buffer (void   *s,
                               size_t  n)
{
#if (defined(__GLIBC__) && defined(__GLIBC_MINOR__) && \
    (__GLIBC__ > 2 || (__GLIBC__ == 2 && __GLIBC_MINOR__ >= 25)) && \
    (defined(_GNU_SOURCE) || defined(_DEFAULT_SOURCE))) || \
    defined(__FreeBSD__) || defined(__OpenBSD__) || defined(__NetBSD__) || defined(__APPLE__)
        explicit_bzero (s, n);
#else
        memset (s, 0, n);
        __asm__ __volatile__ ("" : : "r"(s) : "memory");
#endif
}

static inline GdmPamExtensionMessage *
gdm_pam_extension_message_from_pam_message (const struct pam_message *query)
{
        return (GdmPamExtensionMessage *) (void *) query->msg;
}

static inline char *
gdm_pam_extension_message_to_pam_reply (void *msg)
{
        return (char *) msg;
}

static inline void
gdm_pam_extension_message_to_binary_prompt_message (GdmPamExtensionMessage *extended_message,
                                                    struct pam_message     *binary_message)
{
        binary_message->msg_style = PAM_BINARY_PROMPT;
        binary_message->msg = (void *) extended_message;
}

static inline bool
gdm_pam_extension_message_truncated (const GdmPamExtensionMessage *msg)
{
        return be32toh (msg->length) < sizeof (GdmPamExtensionMessage);
}

static inline bool
gdm_pam_extension_message_invalid_type (const GdmPamExtensionMessage *msg)
{
        const char *env;
        const char *p;
        int count = 0;

        env = getenv ("GDM_SUPPORTED_PAM_EXTENSIONS");
        if (env == NULL)
                return true;

        p = env;
        while (*p != '\0' && count <= UCHAR_MAX) {
                size_t len = strcspn (p, " ");
                if (len > 0)
                        count++;
                p += len;
                p += strspn (p, " ");
        }

        return msg->type >= count;
}

static inline bool
gdm_pam_extension_message_match (const GdmPamExtensionMessage *msg,
                                 char * const                 *supported_extensions,
                                 const char                   *name)
{
        return strcmp (supported_extensions[msg->type], name) == 0;
}

static inline bool
gdm_pam_extension_look_up_type (const char    *name,
                                unsigned char *extension_type)
{
        const char *env;
        const char *p;
        size_t index = 0;

        env = getenv ("GDM_SUPPORTED_PAM_EXTENSIONS");
        if (env == NULL)
                return false;

        p = env;
        while (*p != '\0') {
                size_t len = strcspn (p, " ");

                if (len > 0 && strncmp (p, name, len) == 0 && name[len] == '\0') {
                        if (extension_type != NULL)
                                *extension_type = index;
                        return true;
                }

                p += len;
                p += strspn (p, " ");

                if (index >= UCHAR_MAX)
                        break;
                if (len > 0)
                        index++;
        }

        return false;
}

static inline bool
gdm_pam_extension_supported (const char *name)
{
        return gdm_pam_extension_look_up_type (name, NULL);
}

/* environment_block should be a statically allocated chunk of memory. This is
 * important because putenv() will leak otherwise (and setenv isn't thread safe)
 */
static inline void
gdm_pam_extension_advertise_supported_extensions (char               *environment_block,
                                                  size_t              block_size,
                                                  const char * const *supported_extensions)
{
        const char *key = "GDM_SUPPORTED_PAM_EXTENSIONS";
        size_t key_len = strlen (key);
        size_t offset;
        size_t index;

        if (block_size < key_len + 2)
                return;

        memcpy (environment_block, key, key_len);
        environment_block[key_len] = '=';
        offset = key_len + 1;

        for (index = 0; supported_extensions[index] != NULL && index < UCHAR_MAX; index++) {
                size_t ext_len = strlen (supported_extensions[index]);
                size_t needed = ext_len + (index > 0 ? 1 : 0);

                if (offset + needed + 1 > block_size)
                        break;

                if (index > 0)
                        environment_block[offset++] = ' ';

                memcpy (environment_block + offset, supported_extensions[index], ext_len);
                offset += ext_len;
        }

        if (index > 0) {
                environment_block[offset] = '\0';
                putenv (environment_block);
        }
}
