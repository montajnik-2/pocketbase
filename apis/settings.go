package apis

import (
	"log"
	"net/http"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/labstack/echo/v5"
	"github.com/montajnik-2/pocketbase/core"
	"github.com/montajnik-2/pocketbase/forms"
	"github.com/montajnik-2/pocketbase/models/settings"
)

// bindSettingsApi registers the settings api endpoints.
func bindSettingsApi(app core.App, rg *echo.Group) {
	api := settingsApi{app: app}

	subGroup := rg.Group("/settings", ActivityLogger(app), RequireAdminAuth())
	subGroup.GET("", api.list)
	subGroup.PATCH("", api.set)
	subGroup.POST("/test/s3", api.testS3)
	subGroup.POST("/test/email", api.testEmail)
	subGroup.POST("/apple/generate-client-secret", api.generateAppleClientSecret)
}

type settingsApi struct {
	app core.App
}

func (api *settingsApi) list(c echo.Context) error {
	settings, err := api.app.Settings().RedactClone()
	if err != nil {
		return NewBadRequestError("", err)
	}

	event := new(core.SettingsListEvent)
	event.HttpContext = c
	event.RedactedSettings = settings

	return api.app.OnSettingsListRequest().Trigger(event, func(e *core.SettingsListEvent) error {
		return e.HttpContext.JSON(http.StatusOK, e.RedactedSettings)
	})
}

func (api *settingsApi) set(c echo.Context) error {
	form := forms.NewSettingsUpsert(api.app)

	// load request
	if err := c.Bind(form); err != nil {
		return NewBadRequestError("An error occurred while loading the submitted data.", err)
	}

	event := new(core.SettingsUpdateEvent)
	event.HttpContext = c
	event.OldSettings = api.app.Settings()

	// update the settings
	submitErr := form.Submit(func(next forms.InterceptorNextFunc[*settings.Settings]) forms.InterceptorNextFunc[*settings.Settings] {
		return func(s *settings.Settings) error {
			event.NewSettings = s

			return api.app.OnSettingsBeforeUpdateRequest().Trigger(event, func(e *core.SettingsUpdateEvent) error {
				if err := next(e.NewSettings); err != nil {
					return NewBadRequestError("An error occurred while submitting the form.", err)
				}

				redactedSettings, err := api.app.Settings().RedactClone()
				if err != nil {
					return NewBadRequestError("", err)
				}

				return e.HttpContext.JSON(http.StatusOK, redactedSettings)
			})
		}
	})

	if submitErr == nil {
		if err := api.app.OnSettingsAfterUpdateRequest().Trigger(event); err != nil && api.app.IsDebug() {
			log.Println(err)
		}
	}

	return submitErr
}

func (api *settingsApi) testS3(c echo.Context) error {
	form := forms.NewTestS3Filesystem(api.app)

	// load request
	if err := c.Bind(form); err != nil {
		return NewBadRequestError("An error occurred while loading the submitted data.", err)
	}

	// send
	if err := form.Submit(); err != nil {
		// form error
		if fErr, ok := err.(validation.Errors); ok {
			return NewBadRequestError("Failed to test the S3 filesystem.", fErr)
		}

		// mailer error
		return NewBadRequestError("Failed to test the S3 filesystem. Raw error: \n"+err.Error(), nil)
	}

	return c.NoContent(http.StatusNoContent)
}

func (api *settingsApi) testEmail(c echo.Context) error {
	form := forms.NewTestEmailSend(api.app)

	// load request
	if err := c.Bind(form); err != nil {
		return NewBadRequestError("An error occurred while loading the submitted data.", err)
	}

	// send
	if err := form.Submit(); err != nil {
		// form error
		if fErr, ok := err.(validation.Errors); ok {
			return NewBadRequestError("Failed to send the test email.", fErr)
		}

		// mailer error
		return NewBadRequestError("Failed to send the test email. Raw error: \n"+err.Error(), nil)
	}

	return c.NoContent(http.StatusNoContent)
}

func (api *settingsApi) generateAppleClientSecret(c echo.Context) error {
	form := forms.NewAppleClientSecretCreate(api.app)

	// load request
	if err := c.Bind(form); err != nil {
		return NewBadRequestError("An error occurred while loading the submitted data.", err)
	}

	// generate
	secret, err := form.Submit()
	if err != nil {
		// form error
		if fErr, ok := err.(validation.Errors); ok {
			return NewBadRequestError("Invalid client secret data.", fErr)
		}

		// secret generation error
		return NewBadRequestError("Failed to generate client secret. Raw error: \n"+err.Error(), nil)
	}

	return c.JSON(http.StatusOK, map[string]any{
		"secret": secret,
	})
}
