package gdm

/*
// FIXME: Use pkg-config to include extension protocol headers once available
//#cgo pkg-config: gdm-pam-extensions
#cgo CFLAGS: -Wno-stringop-overread
#include "extension.h"
*/
import "C"

import (
	"encoding/json"
	"errors"
	"fmt"
	"unsafe"

	"github.com/msteinert/pam/v2"
)

const (
	// PamExtensionCustomJSON is the gdm PAM extension for passing string values.
	PamExtensionCustomJSON = C.GDM_PAM_EXTENSION_CUSTOM_JSON
	// JSONProtoVersion is the gdm private string protocol version.
	JSONProtoVersion = uint(C.JSON_PROTO_VERSION)

	jsonProtoMessageSize = C.GDM_PAM_EXTENSION_CUSTOM_JSON_SIZE
)

var (
	// JSONProtoName is the gdm private string protocol name.
	JSONProtoName = C.GoString(C.JSON_PROTO_NAME)
)

// ErrProtoNotSupported is an error used when protocol/version is not supported.
var ErrProtoNotSupported = errors.New("protocol not supported")

// ErrInvalidJSON is an error used when processed JSON is not valid.
var ErrInvalidJSON = errors.New("invalid JSON")

func validateJSONDebug(jsonValue []byte) error {
	if !json.Valid(jsonValue) {
		return ErrInvalidJSON
	}
	return nil
}

func validateJSONDisabled(jsonValue []byte) error {
	return nil
}

var validateJSONFunc = validateJSONDisabled

// IsPamExtensionSupported returns if the provided extension is supported.
func IsPamExtensionSupported(extension string) bool {
	cExtension := C.CString(extension)
	defer C.free(unsafe.Pointer(cExtension))
	return bool(C.is_gdm_pam_extension_supported(cExtension))
}

// AdvertisePamExtensions enable GDM pam extensions in the current binary.
func AdvertisePamExtensions(extensions []string) {
	if len(extensions) == 0 {
		C.gdm_extensions_advertise_supported(nil, 0)
		return
	}
	cArray := make([]*C.char, 0, len(extensions))
	for _, extension := range extensions {
		cExtension := C.CString(extension)
		defer C.free(unsafe.Pointer(cExtension))
		cArray = append(cArray, cExtension)
	}
	C.gdm_extensions_advertise_supported(&cArray[0], C.size_t(len(extensions)))
}

type jsonProtoMessage C.GdmPamExtensionJSONProtocol

func allocateJSONProtoMessage() *jsonProtoMessage {
	// We do manual memory management here, instead of returning a go-allocated
	// structure, so that we can just use a single finalizer function for both
	// request and response messages.
	var msg *jsonProtoMessage
	msg = (*jsonProtoMessage)(C.malloc(C.size_t(unsafe.Sizeof(*msg))))
	return msg
}

func newJSONProtoMessage(jsonValue []byte) (*jsonProtoMessage, error) {
	if err := validateJSONFunc(jsonValue); err != nil {
		return nil, err
	}
	msg := allocateJSONProtoMessage()
	msg.initAuthd(jsonValue)
	return msg, nil
}

func (msg *jsonProtoMessage) initAuthd(jsonValue []byte) {
	cJSON := (*C.char)(nil)
	if jsonValue != nil {
		// We don't use string() here to avoid an extra copy, so we need to
		// add ourself the final null byte to the string.
		// Also the newly allocated C bytes are stolen, since they will be
		// owned by the jsonProtoMessage now. So it's up to it to release
		// them via finalizer functions.
		cJSON = (*C.char)(C.CBytes(append(jsonValue, 0)))
	}
	C.gdm_custom_json_request_init_authd((*C.GdmPamExtensionJSONProtocol)(msg),
		cJSON)
}

func (msg *jsonProtoMessage) initFull(protoName string, protoVersion uint, jsonValue []byte) {
	cProto := C.CString(protoName)
	defer C.free(unsafe.Pointer(cProto))
	cJSON := (*C.char)(nil)
	if jsonValue != nil {
		cJSON = C.CString(string(jsonValue))
	}
	C.gdm_custom_json_request_init((*C.GdmPamExtensionJSONProtocol)(msg),
		cProto, C.uint(protoVersion), cJSON)
}

func (msg *jsonProtoMessage) release() {
	if msg == nil {
		return
	}

	C.free(unsafe.Pointer(msg.json))
	C.free(unsafe.Pointer(msg))
}

func (msg *jsonProtoMessage) protoName() string {
	return C.GoString((*C.char)(unsafe.Pointer(&msg.protocol_name)))
}

func (msg *jsonProtoMessage) protoVersion() uint {
	return uint(msg.version)
}

func (msg *jsonProtoMessage) isValidAuthd() bool {
	return bool(C.gdm_custom_json_request_is_valid_authd(
		(*C.GdmPamExtensionJSONProtocol)(msg)))
}

func (msg *jsonProtoMessage) JSON() ([]byte, error) {
	if msg.json == nil {
		return nil, ErrInvalidJSON
	}

	jsonLen := C.strlen(msg.json)
	jsonValue := C.GoBytes(unsafe.Pointer(msg.json), C.int(jsonLen))

	if err := validateJSONFunc(jsonValue); err != nil {
		return nil, err
	}
	return jsonValue, nil
}

func (msg *jsonProtoMessage) encode() pam.BinaryPointer {
	return pam.BinaryPointer(msg)
}

// NewBinaryJSONProtoRequest returns a new pam.BinaryConvRequest from the
// provided data.
func NewBinaryJSONProtoRequest(data []byte) (*pam.BinaryConvRequest, error) {
	request, err := newJSONProtoMessage(data)
	if err != nil {
		return nil, err
	}
	return pam.NewBinaryConvRequest(request.encode(),
		func(ptr pam.BinaryPointer) { (*jsonProtoMessage)(ptr).release() }), nil
}

// decodeJSONProtoMessage decodes a binary pointer into its JSON representation.
func decodeJSONProtoMessage(response pam.BinaryPointer) ([]byte, error) {
	reply := (*jsonProtoMessage)(response)

	if !reply.isValidAuthd() {
		return nil, fmt.Errorf("%w: got %s v%d, expected %s v%d", ErrProtoNotSupported,
			reply.protoName(), reply.protoVersion(), JSONProtoName, JSONProtoVersion)
	}

	return reply.JSON()
}
