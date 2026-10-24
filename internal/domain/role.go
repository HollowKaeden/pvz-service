package domain

// Role — роль пользователя: от неё зависит, какие действия ему доступны.
type Role string

// Роли из контракта API. В тексте задания обычный пользователь назван client,
// но источник истины — swagger, а в нём employee.
const (
	RoleEmployee  Role = "employee"
	RoleModerator Role = "moderator"
)

// Valid сообщает, что такая роль существует.
func (r Role) Valid() bool {
	return r == RoleEmployee || r == RoleModerator
}
