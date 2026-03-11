# Research: SmartSDR DAX TCP API Protocol and Secondary Connections
Started: 2026-03-09T15:06:04-07:00 | Status: in_progress

## Problem
A Go TCP proxy (Flexy) proxies SmartSDR connections to a FlexRadio radio. The main TCP connection on port 4992 works (full SmartSDR handshake including subscriptions visible), but the SmartSDR client cannot "add a receiver" (panadapter). We need to understand:

1. How DAX (Digital Audio eXchange) protocol works in SmartSDR
2. How slices/panadapters are created via the TCP API
3. What information SmartSDR expects before allowing "add a receiver"
4. Whether there is a separate DAX TCP endpoint that SmartSDR connects to
5. The relationship between port 4992 and any secondary DAX connections
6. How a TCP proxy should handle DAX connections

## Awesome Lists Checked

## Searches

## Sources

## Approaches

## Recommendation

## Implementation

## Risks

METRICS: searches=0 fetches=0 high_quality=0 ratio=0.0
CHECKS: [ ] freshness [ ] went_deep [ ] found_outlier [ ] checked_awesome

## Feedback
usefulness: | implemented: | result: | notes:
