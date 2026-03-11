package main

import "context"

func zero(ctx context.Context, _ []string) (string, error) {
	return "0\n", nil
}
