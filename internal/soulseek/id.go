package soulseek

import "github.com/google/uuid"

// uuidPkgNewString is a thin indirection for tests that want to seed IDs.
var uuidPkgNewString = func() string { return uuid.NewString() }
