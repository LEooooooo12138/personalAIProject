package gateway

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/yuanleyao/ai-agent/internal/console"
)

func (s *Server) handleConsoleMembers(c *gin.Context) {
	users, err := s.consoleStore.ListMembers()
	if err != nil {
		consoleStoreError(c, err)
		return
	}
	c.JSON(200, gin.H{"members": users})
}
func (s *Server) handleConsoleCreateMember(c *gin.Context) {
	var input struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
	}
	if !decodeConsoleJSON(c, &input) {
		return
	}
	user, temp, err := s.consoleStore.CreateMember(input.Username, input.DisplayName)
	if err != nil {
		consoleStoreError(c, err)
		return
	}
	c.JSON(201, gin.H{"user": user, "temporary_password": temp})
}
func (s *Server) handleConsoleUpdateMember(c *gin.Context) {
	var input struct {
		DisplayName *string `json:"display_name"`
		Disabled    *bool   `json:"disabled"`
	}
	if !decodeConsoleJSON(c, &input) {
		return
	}
	if input.DisplayName == nil && input.Disabled == nil {
		consoleError(c, 422, "invalid_request", "Supply at least one member field")
		return
	}
	// Store.UpdateMember accepts a full pair. Serialize the read/merge/write so
	// concurrent partial HTTP updates cannot restore an old omitted field.
	s.consoleMemberMu.Lock()
	defer s.consoleMemberMu.Unlock()
	user, err := s.consoleStore.GetUser(c.Param("id"))
	if err != nil {
		consoleMemberLookupError(c, err)
		return
	}
	if user.Role != "member" {
		consoleError(c, 403, "forbidden", "Administrator cannot be modified here")
		return
	}
	if input.DisplayName != nil {
		user.DisplayName = *input.DisplayName
	}
	if input.Disabled != nil {
		user.Disabled = *input.Disabled
	}
	user, err = s.consoleStore.UpdateMember(user.ID, user.DisplayName, user.Disabled)
	if err != nil {
		consoleStoreError(c, err)
		return
	}
	c.JSON(200, gin.H{"user": user})
}
func (s *Server) handleConsoleResetMember(c *gin.Context) {
	user, err := s.consoleStore.GetUser(c.Param("id"))
	if err != nil {
		consoleMemberLookupError(c, err)
		return
	}
	if user.Role != "member" {
		consoleError(c, 403, "forbidden", "Administrator cannot be modified here")
		return
	}
	temp, err := s.consoleStore.ResetMemberPassword(user.ID)
	if err != nil {
		consoleStoreError(c, err)
		return
	}
	c.JSON(200, gin.H{"temporary_password": temp})
}

func consoleMemberLookupError(c *gin.Context, err error) {
	if errors.Is(err, console.ErrInvalid) {
		consoleError(c, 404, "not_found", "Member not found")
		return
	}
	consoleStoreError(c, err)
}
