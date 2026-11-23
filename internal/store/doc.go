// Package store keeps immutable snapshots of containers, images and volumes,
// refreshed by the engine's event stream, and delivers them to the UI as
// Bubble Tea messages. Views read state from the store and never call the
// engine directly for list data.
package store
