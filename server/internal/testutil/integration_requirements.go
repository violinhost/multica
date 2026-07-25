package testutil

import "os"

func RequireTestDB() bool {
	return os.Getenv("MULTICA_REQUIRE_TEST_DB") == "1" || os.Getenv("GITHUB_ACTIONS") == "true"
}

func RequireTestRedis() bool {
	return os.Getenv("MULTICA_REQUIRE_TEST_REDIS") == "1" || os.Getenv("GITHUB_ACTIONS") == "true"
}
