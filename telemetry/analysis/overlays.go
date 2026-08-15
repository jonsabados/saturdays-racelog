package analysis

// Per-track corner overlays — canonical, named corner lists that stay stable
// across sessions regardless of how a given lap was driven (a tire-saving lift
// leaves no speed dip, so pure detection is lap-dependent) — are intentionally
// deferred: the tool currently relies on auto-detected corners. When overlays
// land, they live here.
