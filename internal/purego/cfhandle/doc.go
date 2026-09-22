// Package cfhandle operates on retained foreign CoreFoundation handles without
// converting their integer representation to Go pointers. Handles must refer to
// live OS objects, never Go allocations. Call Open before acquiring resources
// whose cleanup depends on this package.
package cfhandle
