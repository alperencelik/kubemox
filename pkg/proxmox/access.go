package proxmox

import (
	"fmt"
	"reflect"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/utils"
	"github.com/luthermonson/go-proxmox"
)

const (
	randomPasswordLength = 12
)

func DeleteUser(userID string) error {
	user, err := Client.User(ctx, userID)
	if err != nil {
		return fmt.Errorf("unable to get user: %v", err)
	}
	err = user.Delete(ctx)
	if err != nil {
		return fmt.Errorf("unable to delete user: %v", err)
	}
	return nil
}

func CreateUser(proxmoxUser *proxmoxv1alpha1.User) (err error) {
	userSpec := proxmoxUser.Spec
	expire, err := utils.ConvertToUnixTime(userSpec.Expire)
	if err != nil {
		return err
	}
	user := proxmox.NewUser{
		UserID:    userSpec.UserID,
		Comment:   userSpec.Comment,
		Email:     userSpec.Email,
		Enable:    userSpec.Enable,
		Expire:    int(expire), // 0 means no expiration
		Firstname: userSpec.Firstname,
		Groups:    userSpec.Groups,
		Keys:      userSpec.Keys,
		Lastname:  userSpec.Lastname,
		Password:  "",
	}
	err = Client.NewUser(ctx, &user)
	if err != nil {
		return fmt.Errorf("unable to create user: %v", err)
	}
	if userSpec.Password != "" {
		err = Client.Password(ctx, user.UserID, userSpec.Password)
		if err != nil {
			return fmt.Errorf("unable to update user password: %v", err)
		}
	}
	return err
}

func GetUsers() (proxmox.Users, error) {
	users, err := Client.Users(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to get users: %v", err)
	}
	return users, nil
}

func UserExists(userID string) (bool, error) {
	users, err := GetUsers()
	if err != nil {
		return false, fmt.Errorf("unable to get users: %v", err)
	}
	for _, user := range users {
		if user.UserID == userID {
			return true, nil
		}
	}
	return false, nil
}

func UpdateUser(userSpec *proxmoxv1alpha1.UserSpec) error {
	// Get user
	actualUser, err := Client.User(ctx, userSpec.UserID)
	if err != nil {
		return fmt.Errorf("unable to get user: %v", err)
	}
	// Convert int to string

	actualUserSpec := &proxmoxv1alpha1.UserSpec{
		UserID:    actualUser.UserID,
		Comment:   actualUser.Comment,
		Email:     actualUser.Email,
		Enable:    bool(actualUser.Enable),
		Expire:    "",
		Firstname: actualUser.Firstname,
		Groups:    actualUser.Groups,
		Keys:      actualUser.Keys,
		Lastname:  actualUser.Lastname,
	}
	if !reflect.DeepEqual(actualUserSpec, userSpec) {
		// Update user with the new spec
		expire, err := utils.ConvertToUnixTime(userSpec.Expire)
		if err != nil {
			return err
		}
	
}

func UpdatePassword(proxmoxUser *proxmoxv1alpha1.User) (string, error) {

	user, err := Client.User(ctx, proxmoxUser.Spec.UserID)
	if err != nil {
		return "", fmt.Errorf("unable to get user: %v", err)
	}
	var password string
	// Update password of the user
	if proxmoxUser.Spec.Password == "" {
		// Generate a random password
		password = utils.GenerateRandomPassword(randomPasswordLength)
		if err != nil {
			return "", fmt.Errorf("unable to generate random password: %v", err)
		}
		// Update the user spec with the generated password
		proxmoxUser.Spec.Password = password
		if err != nil {
			return "", fmt.Errorf("unable to update user: %v", err)
		}
	}
	err = Client.Password(ctx, user.UserID, password)
	if err != nil {
		return "", fmt.Errorf("unable to update user password: %v", err)
	}
	return password, err
}
