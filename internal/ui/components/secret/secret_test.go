package secret

import "testing"

func TestIsSecret(t *testing.T) {
	for name, want := range map[string]bool{
		"DB_PASSWORD": true, "POSTGRES_PASSWORD": true, "MYSQL_PWD": true, "API_TOKEN": true,
		"AWS_SECRET_ACCESS_KEY": true, "STRIPE_API_KEY": true, "SSH_PRIVATE_KEY": true, "GH_AUTH": true,
		"environment.DB_PASS_WORD": false, "environment.APP_PASSWORD": true, "KEY": true, "credentials": true,
		"PORT": false, "DB_HOST": false, "PATH": false, "KEYCLOAK_URL": false, "AUTHOR": false, "LOG_LEVEL": false,
	} {
		if got := IsSecret(name); got != want {
			t.Errorf("IsSecret(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestEnv(t *testing.T) {
	if got, ok := Env("DB_PASSWORD=hunter2"); got != "DB_PASSWORD="+Mask || !ok {
		t.Errorf("got %q %v", got, ok)
	}
	if got, ok := Env("PORT=8080"); got != "PORT=8080" || ok {
		t.Errorf("got %q %v", got, ok)
	}
	if got, ok := Env("TOKEN="); got != "TOKEN=" || ok {
		t.Errorf("empty secret masked: %q", got)
	}
}
