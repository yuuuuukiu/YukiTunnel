// The desktop admin console reuses the WebUI page verbatim: both talk to the
// same REST API through the same client, so keeping one implementation avoids
// the two surfaces drifting apart.
export { default } from '@web/pages/admin/Tunnels';
