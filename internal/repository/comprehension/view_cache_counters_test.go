package comprehension

func resetViewCacheCounters() {
	viewCacheHits.Store(0)
	viewCacheMisses.Store(0)
	viewCacheWrites.Store(0)
	viewCacheFreshnessHits.Store(0)
	viewCacheStalePreventions.Store(0)
	viewCacheStaleRowMisses.Store(0)
	estimatedFullTokensSaved.Store(0)
	estimatedRediscoveryTokensAvoided.Store(0)
}
