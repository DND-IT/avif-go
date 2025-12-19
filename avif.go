// Package avif is a Go library and CLI tool to encode/decode AVIF images without system dependencies (CGO).
package avif

/*
#include <stdlib.h>
#include <avif/avif.h>

// Helper to get error string from avifResult
const char* get_error_string(avifResult result) {
    return avifResultToString(result);
}

// Full decode: creates a decoder, sets up the memory I/O, and decodes the image.
// Returns the avifImage pointer (which contains width, height, etc.) and leaves the
// decoder pointer for cleanup. Returns error result via outResult.
avifImage* decode_avif_image(const uint8_t * data, size_t size, avifDecoder ** outDecoder, avifResult *outResult) {
    avifDecoder* decoder = avifDecoderCreate();
    // Force libavif to use the dav1d backend.
    decoder->codecChoice = AVIF_CODEC_CHOICE_DAV1D;

    *outResult = avifDecoderSetIOMemory(decoder, data, size);
    if (*outResult != AVIF_RESULT_OK) {
        avifDecoderDestroy(decoder);
        return NULL;
    }

    *outResult = avifDecoderParse(decoder);
    if (*outResult != AVIF_RESULT_OK) {
        avifDecoderDestroy(decoder);
        return NULL;
    }

    *outResult = avifDecoderNextImage(decoder);
    if (*outResult != AVIF_RESULT_OK) {
        avifDecoderDestroy(decoder);
        return NULL;
    }

    if (outDecoder) {
        *outDecoder = decoder;
    }
    return decoder->image;
}

// Config-only decode: reads the header and returns width and height.
// Returns error result via outResult.
void get_avif_config(const uint8_t * data, size_t size, uint32_t * width, uint32_t * height, avifResult *outResult) {
    avifDecoder* decoder = avifDecoderCreate();
    // Force libavif to use the dav1d backend.
    decoder->codecChoice = AVIF_CODEC_CHOICE_DAV1D;

    *outResult = avifDecoderSetIOMemory(decoder, data, size);
    if (*outResult != AVIF_RESULT_OK) {
         *width = 0;
         *height = 0;
         avifDecoderDestroy(decoder);
         return;
    }

    *outResult = avifDecoderParse(decoder);
    if (*outResult != AVIF_RESULT_OK) {
         *width = 0;
         *height = 0;
         avifDecoderDestroy(decoder);
         return;
    }

    *width = decoder->image->width;
    *height = decoder->image->height;
    avifDecoderDestroy(decoder);
}
*/
import "C"
import (
	"fmt"
	"image"
	"image/color"
	"unsafe"
)

// encodeAVIF encodes an RGBA image to AVIF format.
//
// Speed ranges from 0 (slowest, best quality) to 10 (fastest, lower quality).
//
// ColorQuality and AlphaQuality range from 0 (worst) to 100 (lossless).
func encodeAVIF(rgba image.RGBA, options Options) ([]byte, error) {
	width := rgba.Bounds().Dx()
	height := rgba.Bounds().Dy()

	if width == 0 || height == 0 {
		return nil, fmt.Errorf("invalid image dimensions: %dx%d", width, height)
	}

	// Create an avifImage for the output.
	// Here we use 8 bits per channel and the YUV420 pixel format.
	avifImage := C.avifImageCreate(C.uint32_t(width), C.uint32_t(height), 8, C.AVIF_PIXEL_FORMAT_YUV420)
	if avifImage == nil {
		return nil, fmt.Errorf("failed to create AVIF image")
	}

	// Ensure the image memory is freed later
	defer C.avifImageDestroy(avifImage)

	// Allocate avifRGBImage on the C heap to avoid passing a pointer to a Go-allocated variable.
	rgb := (*C.avifRGBImage)(C.malloc(C.size_t(unsafe.Sizeof(C.avifRGBImage{}))))
	if rgb == nil {
		return nil, fmt.Errorf("failed to allocate avifRGBImage")
	}

	defer C.free(unsafe.Pointer(rgb))

	// Set defaults and fill in the fields.
	C.avifRGBImageSetDefaults(rgb, avifImage)
	rgb.format = C.AVIF_RGB_FORMAT_RGBA
	rgb.depth = 8
	rgb.pixels = (*C.uint8_t)(unsafe.Pointer(&rgba.Pix[0]))

	// Explicitly cast the stride to C.uint32_t
	rgb.rowBytes = C.uint32_t(rgba.Stride)

	// Convert the RGB image to the YUV image required for AVIF
	if C.avifImageRGBToYUV(avifImage, rgb) != C.AVIF_RESULT_OK {
		return nil, fmt.Errorf("failed to convert image from RGB to YUV")
	}

	// Create an AVIF encoder instance
	encoder := C.avifEncoderCreate()
	if encoder == nil {
		return nil, fmt.Errorf("failed to create AVIF encoder")
	}

	// Make sure to clean up the encoder when done.
	defer C.avifEncoderDestroy(encoder)

	// Set SVT-AV1 as the backend.
	encoder.codecChoice = C.AVIF_CODEC_CHOICE_SVT

	// Optionally, adjust encoder parameters
	encoder.speed = C.int(options.Speed)
	encoder.quality = C.int(options.ColorQuality)
	encoder.qualityAlpha = C.int(options.AlphaQuality)

	// Initialize an avifRWData structure to hold the encoded data.
	var encodedData C.avifRWData
	encodedData.data = nil
	encodedData.size = 0

	// Encode the image
	result := C.avifEncoderWrite(encoder, avifImage, &encodedData)
	if result != C.AVIF_RESULT_OK {
		errStr := C.GoString(C.get_error_string(result))
		return nil, fmt.Errorf("failed to encode AVIF image: %s", errStr)
	}
	// Ensure the allocated AVIF data is freed later
	defer C.avifRWDataFree(&encodedData)

	// Convert the C buffer to a Go byte slice
	data := C.GoBytes(unsafe.Pointer(encodedData.data), C.int(encodedData.size))
	return data, nil
}

// decodeAVIFToRGBA decodes AVIF image data to an RGBA image.
func decodeAVIFToRGBA(data []byte) (*image.RGBA, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("cannot decode empty data")
	}

	// Allocate C memory and copy data.
	cData := C.CBytes(data)
	defer C.free(cData)

	var decoder *C.avifDecoder
	var result C.avifResult
	avifImg := C.decode_avif_image((*C.uint8_t)(cData), C.size_t(len(data)), &decoder, &result)
	if avifImg == nil {
		errStr := C.GoString(C.get_error_string(result))
		return nil, fmt.Errorf("failed to decode AVIF image: %s", errStr)
	}
	defer C.avifDecoderDestroy(decoder)

	// Set up an avifRGBImage struct to hold the converted image.
	var rgb C.avifRGBImage
	C.avifRGBImageSetDefaults(&rgb, avifImg)
	rgb.format = C.AVIF_RGB_FORMAT_RGBA
	rgb.depth = 8 // 8-bit per channel

	// Allocate pixel buffer for the RGB data.
	if C.avifRGBImageAllocatePixels(&rgb) != C.AVIF_RESULT_OK {
		return nil, fmt.Errorf("failed to allocate RGB pixels")
	}
	defer C.avifRGBImageFreePixels(&rgb)

	// Convert the image from YUV to RGB.
	result = C.avifImageYUVToRGB(avifImg, &rgb)
	if result != C.AVIF_RESULT_OK {
		errStr := C.GoString(C.get_error_string(result))
		return nil, fmt.Errorf("failed to convert image to RGB: %s", errStr)
	}

	width := int(avifImg.width)
	height := int(avifImg.height)
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	rowBytes := int(rgb.rowBytes)

	// Copy the pixel data row by row into the Go image using direct pointer access.
	// This avoids the extra allocation from C.GoBytes for the entire buffer.
	for y := 0; y < height; y++ {
		srcPtr := unsafe.Add(unsafe.Pointer(rgb.pixels), y*rowBytes)
		dstOffset := y * img.Stride
		copy(img.Pix[dstOffset:dstOffset+4*width],
			unsafe.Slice((*byte)(srcPtr), 4*width))
	}

	return img, nil
}

// decodeConfig reads enough of the data to determine the image's configuration (dimensions, etc.).
//
// This is a lightweight operation that only parses the header.
func decodeConfig(data []byte) (image.Config, error) {
	if len(data) == 0 {
		return image.Config{}, fmt.Errorf("failed to get AVIF image config: empty data")
	}

	// Use C.CBytes for safer memory handling
	cData := C.CBytes(data)
	defer C.free(cData)

	var width, height C.uint32_t
	var result C.avifResult
	C.get_avif_config((*C.uint8_t)(cData), C.size_t(len(data)), &width, &height, &result)

	if result != C.AVIF_RESULT_OK {
		errStr := C.GoString(C.get_error_string(result))
		return image.Config{}, fmt.Errorf("failed to get AVIF image config: %s", errStr)
	}

	if width == 0 || height == 0 {
		return image.Config{}, fmt.Errorf("invalid image dimensions: %dx%d", width, height)
	}

	// We assume an RGBA color model for simplicity.
	return image.Config{
		ColorModel: color.RGBAModel,
		Width:      int(width),
		Height:     int(height),
	}, nil
}
