package httpapi

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/smsverify"
	"github.com/qingyingjin/banking-service/internal/userauth"
)

const refreshCookieName = "qingrongjin_refresh"
const refreshCookiePath = "/api/v1/auth"

type phoneAuthSettings struct {
	service *userauth.Service
	secure  bool
}

// Authentication cookies are used only by these endpoints. Business requests
// continue using the existing short-lived Bearer identity and financial gateway.
func registerPhoneAuth(r *gin.Engine, auth *identity.Auth, settings phoneAuthSettings) {
	r.GET("/api/v1/auth/capabilities", func(c *gin.Context) {
		mode := "disabled"
		if settings.service != nil {
			mode = settings.service.SMSMode()
		}
		enabled := mode != "disabled"
		respond(c, 200, "OK", "认证能力", gin.H{"sms_enabled": enabled, "sms_mode": mode, "registration_verification_required": enabled, "code_expires_in": 300, "resend_after": 60})
	})
	group := r.Group("/api/v1/auth")
	group.Use(func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		cookie, _ := c.Cookie(refreshCookieName)
		requireOrigin := cookie != "" || strings.HasSuffix(c.Request.URL.Path, "/refresh") || strings.HasSuffix(c.Request.URL.Path, "/logout")
		if !validAuthOrigin(origin, c.Request.Host, settings.secure, requireOrigin) || c.GetHeader("Sec-Fetch-Site") == "cross-site" {
			respond(c, 403, "FORBIDDEN", "登录请求必须来自当前应用", nil)
			c.Abort()
			return
		}
		mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if err != nil || mediaType != "application/json" {
			respond(c, 400, "INVALID_ARGUMENT", "请使用 JSON 提交登录请求", nil)
			c.Abort()
			return
		}
		c.Header("Pragma", "no-cache")
		c.Next()
	})
	configured := func(c *gin.Context) bool {
		if settings.service == nil {
			respond(c, 503, "DEPENDENCY_UNAVAILABLE", "账户登录服务暂不可用", nil)
			return false
		}
		return true
	}
	finish := func(c *gin.Context, status int, session userauth.Session) {
		setRefreshCookie(c, session.RefreshToken, settings.secure)
		c.Set("actor", identity.Actor{Kind: "user", UserID: strconv.FormatInt(session.User.ID, 10)})
		respond(c, status, "OK", "登录成功", session)
	}
	currentUser := func(c *gin.Context) (identity.Actor, bool) {
		raw := c.GetHeader("Authorization")
		if !strings.HasPrefix(raw, "Bearer ") {
			respond(c, 401, "UNAUTHENTICATED", "请先登录本人账户", nil)
			return identity.Actor{}, false
		}
		actor, err := auth.UserContext(c.Request.Context(), strings.TrimPrefix(raw, "Bearer "))
		if err != nil {
			respond(c, 401, "UNAUTHENTICATED", "登录已失效，请重新登录", nil)
			return actor, false
		}
		if actor.Kind != "user" {
			respond(c, 403, "FORBIDDEN", "仅用户本人可验证手机号", nil)
			return actor, false
		}
		c.Set("actor", actor)
		return actor, true
	}
	for path, purpose := range map[string]smsverify.Purpose{"/sms/registration": smsverify.RegisterPhone, "/sms/password-reset": smsverify.ResetPassword} {
		p := purpose
		group.POST(path, func(c *gin.Context) {
			if !configured(c) {
				return
			}
			var in struct {
				Phone string `json:"phone"`
			}
			if !decodeAuthBody(c, &in) {
				return
			}
			var result smsverify.Challenge
			var err error
			if p == smsverify.RegisterPhone {
				result, err = settings.service.RequestRegistrationCode(c.Request.Context(), in.Phone, c.ClientIP())
			} else {
				result, err = settings.service.RequestPasswordReset(c.Request.Context(), in.Phone, c.ClientIP())
			}
			if err != nil {
				phoneAuthError(c, err)
				return
			}
			respond(c, 200, "OK", "验证码请求已受理，请查看对应收件通道", result)
		})
	}
	group.POST("/sms/phone-verification", func(c *gin.Context) {
		actor, ok := currentUser(c)
		if !ok || !configured(c) {
			return
		}
		var in struct{}
		if !decodeAuthBody(c, &in) {
			return
		}
		result, err := settings.service.RequestPhoneVerification(c.Request.Context(), actor, c.ClientIP())
		if err != nil {
			phoneAuthError(c, err)
			return
		}
		respond(c, 200, "OK", "验证码请求已受理", result)
	})
	group.POST("/phone/verify", func(c *gin.Context) {
		actor, ok := currentUser(c)
		if !ok || !configured(c) {
			return
		}
		var in userauth.PhoneVerificationInput
		if !decodeAuthBody(c, &in) {
			return
		}
		if err := settings.service.ConfirmPhoneVerification(c.Request.Context(), actor, in, c.ClientIP()); err != nil {
			phoneAuthError(c, err)
			return
		}
		respondAuthNull(c, "手机号验证完成")
	})
	group.POST("/password/reset", func(c *gin.Context) {
		if !configured(c) {
			return
		}
		var in userauth.PasswordResetInput
		if !decodeAuthBody(c, &in) {
			return
		}
		if err := settings.service.ResetPassword(c.Request.Context(), in, c.ClientIP()); err != nil {
			phoneAuthError(c, err)
			return
		}
		clearRefreshCookie(c, settings.secure)
		respondAuthNull(c, "密码已重置，请使用新密码登录")
	})
	group.POST("/register", func(c *gin.Context) {
		if !configured(c) {
			return
		}
		var input userauth.RegisterInput
		if !decodeAuthBody(c, &input) {
			return
		}
		result, err := settings.service.Register(c.Request.Context(), input, c.ClientIP())
		if err != nil {
			phoneAuthError(c, err)
			return
		}
		finish(c, 201, result)
	})
	group.POST("/login", func(c *gin.Context) {
		if !configured(c) {
			return
		}
		var input userauth.Credentials
		if !decodeAuthBody(c, &input) {
			return
		}
		result, err := settings.service.Login(c.Request.Context(), input, c.ClientIP())
		if err != nil {
			phoneAuthError(c, err)
			return
		}
		finish(c, 200, result)
	})
	group.POST("/bind", func(c *gin.Context) {
		raw := c.GetHeader("Authorization")
		if !strings.HasPrefix(raw, "Bearer ") {
			respond(c, 401, "UNAUTHENTICATED", "请先验证原账户身份", nil)
			return
		}
		actor, err := auth.UserContext(c.Request.Context(), strings.TrimPrefix(raw, "Bearer "))
		if err != nil {
			respond(c, 401, "UNAUTHENTICATED", "请先验证原账户身份", nil)
			return
		}
		if actor.Kind != "user" {
			respond(c, 403, "FORBIDDEN", "仅用户本人可以设置登录凭据", nil)
			return
		}
		c.Set("actor", actor)
		if !configured(c) {
			return
		}
		var input userauth.Credentials
		if !decodeAuthBody(c, &input) {
			return
		}
		result, err := settings.service.Bind(c.Request.Context(), actor, input, c.ClientIP())
		if err != nil {
			phoneAuthError(c, err)
			return
		}
		finish(c, 200, result)
	})
	group.POST("/refresh", func(c *gin.Context) {
		if !configured(c) {
			return
		}
		var input struct{}
		if !decodeAuthBody(c, &input) {
			return
		}
		cookie, _ := c.Cookie(refreshCookieName)
		result, err := settings.service.Refresh(c.Request.Context(), cookie)
		if err != nil {
			if code := entity.Code(err); code == "AUTH_INVALID_CREDENTIALS" || code == "UNAUTHENTICATED" {
				clearRefreshCookie(c, settings.secure)
			}
			phoneAuthError(c, err)
			return
		}
		// Refresh does not extend the fixed database/cookie session lifetime.
		respond(c, 200, "OK", "登录已恢复", result)
	})
	group.POST("/logout", func(c *gin.Context) {
		if !configured(c) {
			return
		}
		var input struct{}
		if !decodeAuthBody(c, &input) {
			return
		}
		cookie, _ := c.Cookie(refreshCookieName)
		if err := settings.service.Logout(c.Request.Context(), cookie); err != nil {
			phoneAuthError(c, err)
			return
		}
		clearRefreshCookie(c, settings.secure)
		respondAuthNull(c, "已退出登录")
	})
}

func respondAuthNull(c *gin.Context, message string) {
	respond(c, http.StatusOK, "OK", message, json.RawMessage("null"))
}

func validAuthOrigin(raw, host string, secure, required bool) bool {
	if raw == "" {
		return !required
	}
	origin, err := url.Parse(raw)
	expectedScheme := "http"
	if secure {
		expectedScheme = "https"
	}
	return err == nil && origin.Scheme == expectedScheme &&
		strings.EqualFold(origin.Host, host) && origin.User == nil && origin.Path == "" && origin.RawQuery == "" && origin.Fragment == ""
}

func decodeAuthBody(c *gin.Context, out any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	d := json.NewDecoder(c.Request.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		respond(c, 400, "INVALID_ARGUMENT", "登录请求格式不正确", nil)
		return false
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		respond(c, 400, "INVALID_ARGUMENT", "登录请求格式不正确", nil)
		return false
	}
	return true
}

func setRefreshCookie(c *gin.Context, value string, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{Name: refreshCookieName, Value: value, Path: refreshCookiePath, HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteStrictMode, MaxAge: 86400, Expires: time.Now().UTC().Add(24 * time.Hour)})
}
func clearRefreshCookie(c *gin.Context, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{Name: refreshCookieName, Path: refreshCookiePath, HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0).UTC()})
}

func phoneAuthError(c *gin.Context, err error) {
	code := entity.Code(err)
	status := 500
	message := "账户服务暂不可用"
	switch code {
	case "INVALID_ARGUMENT":
		status = 400
		message = "请检查手机号及密码格式（密码为8至128个字符）"
	case "AUTH_INVALID_CREDENTIALS", "UNAUTHENTICATED":
		status = 401
		message = "手机号或密码错误，或登录已失效"
	case "AUTH_CODE_INVALID":
		status = 401
		message = "验证码无效、已过期或已使用，请重新获取；历史未验证账号须先用原密码登录验证手机号"
	case "SMS_UNAVAILABLE":
		status = 503
		message = "验证码服务未启用或暂不可用"
	case "FORBIDDEN":
		status = 403
		message = "仅用户本人可以设置登录凭据"
	case "AUTH_PHONE_UNAVAILABLE", "STATE_CONFLICT":
		status = 409
		message = "该手机号或账户暂不能用于注册，请登录已有账户"
	case "AUTH_RATE_LIMITED":
		status = 429
		message = "尝试次数过多，请稍后再试"
		c.Header("Retry-After", "900")
	case "DEPENDENCY_UNAVAILABLE", "STORAGE_FROZEN":
		status = 503
		message = "账户服务暂不可用，请稍后再试"
	}
	respond(c, status, code, message, nil)
}
