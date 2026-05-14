package middleware

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	apperrors "githubb.com/yeimar-projects/wa-go/app/errors"
	"githubb.com/yeimar-projects/wa-go/app/facades"
	"githubb.com/yeimar-projects/wa-go/app/http/response"
	"githubb.com/yeimar-projects/wa-go/app/models"
)

const ContextInstance = "wa_instance"

func AdminAuth() contractshttp.Middleware {
	return func(ctx contractshttp.Context) {
		apiKey := ctx.Request().Header("apikey")
		if apiKey == "" {
			abortWithAppError(ctx, apperrors.Unauthorized("Authentication required. Provide 'apikey' header."))
			return
		}
		globalKey := facades.Config().GetString("whatsapp.global_api_key")
		if apiKey != globalKey {
			abortWithAppError(ctx, apperrors.Unauthorized("Invalid API key."))
			return
		}
		ctx.Request().Next()
	}
}

func InstanceAuth() contractshttp.Middleware {
	return func(ctx contractshttp.Context) {
		apiKey := ctx.Request().Header("apikey")
		if apiKey == "" {
			apiKey = ctx.Request().Query("apikey")
		}
		if apiKey == "" {
			abortWithAppError(ctx, apperrors.Unauthorized("Authentication required. Provide 'apikey' header or query parameter."))
			return
		}

		var inst models.Instance
		if err := facades.Orm().Query().Where("token", apiKey).First(&inst); err != nil {
			abortWithAppError(ctx, apperrors.Unauthorized("Invalid API key."))
			return
		}

		if inst.ID == "" {
			abortWithAppError(ctx, apperrors.Unauthorized("Invalid API key."))
			return
		}

		ctx.WithValue(ContextInstance, &inst)
		ctx.Request().Next()
	}
}

func GetInstance(ctx contractshttp.Context) *models.Instance {
	return ctx.Value(ContextInstance).(*models.Instance)
}

// abortWithAppError aborts the request with a structured error response.
func abortWithAppError(ctx contractshttp.Context, err *apperrors.AppError) {
	ctx.Request().AbortWithStatusJson(err.HTTPStatus(), response.ErrorResponse{
		Status:  err.HTTPStatus(),
		Code:    string(err.Code),
		Message: err.Message,
	})
}
