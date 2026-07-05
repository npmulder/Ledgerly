package identity

// ProfileUpdatedEventName is published after the company profile changes.
const ProfileUpdatedEventName = "identity.ProfileUpdated"

// ProfileUpdated tells subscribers to refresh company identity from Profile.
type ProfileUpdated struct{}

// Name implements bus.Event.
func (ProfileUpdated) Name() string {
	return ProfileUpdatedEventName
}
