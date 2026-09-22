package appstate_test

import (
	"context"
	"fmt"
	"github.com/tmc/axmcp/internal/computeruse/appstate"
)

func ExampleCheckScreenshotGeometry() {
	err := appstate.CheckScreenshotGeometry(context.Background(), nil, nil)
	fmt.Println(err)
	// Output: screenshot window and metadata required
}
