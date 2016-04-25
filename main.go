package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecr"
	"github.com/rancher/go-rancher/client"
)

// Rancher holds the configuration parameters
type Rancher struct {
	URL       string
	AccessKey string
	SecretKey string
	AWSRegion string
}

func main() {
	vargs := Rancher{
		URL:       os.Getenv("CATTLE_URL"),
		AccessKey: os.Getenv("CATTLE_ACCESS_KEY"),
		SecretKey: os.Getenv("CATTLE_SECRET_KEY"),
		AWSRegion: os.Getenv("AWS_REGION"),
	}

	err := updateEcr(vargs)
	if err != nil {
		fmt.Printf("Error updating ECR, %s\n", err)
	}
	ticker := time.NewTicker(6 * time.Hour)
	for {
		<-ticker.C
		err := updateEcr(vargs)
		if err != nil {
			fmt.Printf("Error updating ECR, %s\n", err)
		}
	}

}

func updateEcr(vargs Rancher) error {
	fmt.Printf("Updating ECR Credentials\n")
	ecrClient := ecr.New(session.New(&aws.Config{
		Region: aws.String(vargs.AWSRegion),
	}))

	resp, err := ecrClient.GetAuthorizationToken(&ecr.GetAuthorizationTokenInput{})
	if err != nil {
		return err
	}

	if len(resp.AuthorizationData) < 1 {
		return errors.New("Request did not return authorization data")
	}

	bytes, err := base64.StdEncoding.DecodeString(*resp.AuthorizationData[0].AuthorizationToken)
	if err != nil {
		fmt.Printf("Error decoding authorization token: %s\n", err)
		return err
	}
	token := string(bytes[:len(bytes)])

	authTokens := strings.Split(token, ":")
	if len(authTokens) != 2 {
		return fmt.Errorf("Authorization token does not contain data in <user>:<password> format: %s", token)
	}

	registryURL, err := url.Parse(*resp.AuthorizationData[0].ProxyEndpoint)
	if err != nil {
		fmt.Printf("Error parsing registry URL: %s\n", err)
		return err
	}

	ecrUsername := authTokens[0]
	ecrPassword := authTokens[1]
	ecrURL := registryURL.Host
	rancher, err := client.NewRancherClient(&client.ClientOpts{
		Url:       vargs.URL,
		AccessKey: vargs.AccessKey,
		SecretKey: vargs.SecretKey,
	})

	if err != nil {
		fmt.Printf("Failed to create rancher client: %s\n", err)
		return err
	}
	registries, err := rancher.Registry.List(&client.ListOpts{})
	if err != nil {
		fmt.Printf("Failed to retrieve registries: %s\n", err)
		return err
	}
	for _, registry := range registries.Data {
		serverAddress, err := url.Parse(registry.ServerAddress)
		if err != nil {
			fmt.Printf("Failed to parse configured registry URL %s\n", registry.ServerAddress)
			break
		}
		registryHost := serverAddress.Host
		if registryHost == "" {
			registryHost = serverAddress.Path
		}
		if registryHost == ecrURL {
			credentials, err := rancher.RegistryCredential.List(&client.ListOpts{
				Filters: map[string]interface{}{
					"registryId": registry.Id,
				},
			})
			if err != nil {
				fmt.Printf("Failed to retrieved registry credentials for id: %s, %s\n", registry.Id, err)
				break
			}
			if len(credentials.Data) != 1 {
				fmt.Printf("No credentials retrieved for registry: %s\n", registry.Id)
				break
			}
			credential := credentials.Data[0]
			_, err = rancher.RegistryCredential.Update(&credential, &client.RegistryCredential{
				PublicValue: ecrUsername,
				SecretValue: ecrPassword,
			})
			if err != nil {
				fmt.Printf("Failed to update registry credential %s, %s\n", credential.Id, err)
			} else {
				fmt.Printf("Successfully updated credentials %s for registry %s; registry address: %s\n", credential.Id, registry.Id, registryHost)
			}
			break
		}
		fmt.Printf("Failed to find configured registry to update for URL %s\n", ecrURL)
	}
	return nil
}
