package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/asaskevich/govalidator"
	"github.com/layer5io/meshery/mesheryctl/internal/cli/root/config"
	"github.com/layer5io/meshery/mesheryctl/pkg/utils"
	"github.com/layer5io/meshery/server/models"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	skipSave   bool   // skip saving the app
	appFile    string // app file
	sourceType string // app file type (manifest / compose)
)

var validSourceTypes []string

var onboardCmd = &cobra.Command{
	Use:   "onboard",
	Short: "Onboard application",
	Long:  `Command will trigger deploy of application`,
	Example: `
// Onboard application by providing file path
mesheryctl app onboard -f [filepath] -s [source type]

Example:
mesheryctl app onboard -f ./application.yml -s "Kubernetes Manifest"

! Refer below image link for usage
* Usage of mesheryctl app onboard
# ![app-onboard-usage](/assets/img/mesheryctl/app-onboard.png)
	`,
	Args: func(_ *cobra.Command, args []string) error {
		const errMsg = `Usage: mesheryctl app onboard -f [filepath] -s [source type]
Example: mesheryctl app onboard -f ./application.yml -s "Kubernetes Manifest"
Description: Onboard application`

		if file == "" && len(args) == 0 {
			return fmt.Errorf("file path or application name not provided \n\n%v", errMsg)
		}
		return nil
	},
	PreRunE: func(cmd *cobra.Command, args []string) error {
		mctlCfg, err := config.GetMesheryCtl(viper.GetViper())
		if err != nil {
			return err
		}
		validTypesURL := mctlCfg.GetBaseMesheryURL() + "/api/application/types"
		client := &http.Client{}
		req, err := utils.NewRequest("GET", validTypesURL, nil)
		if err != nil {
			return err
		}

		resp, err := client.Do(req)
		if err != nil {
			return err
		}

		if resp.StatusCode != 200 {
			return errors.Errorf("Response Status Code %d, possible Server Error", resp.StatusCode)
		}
		defer resp.Body.Close()

		var response []*models.ApplicationSourceTypesAPIResponse

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return errors.Wrap(err, utils.PerfError("failed to read response body"))
		}
		err = json.Unmarshal(body, &response)
		if err != nil {
			return errors.Wrap(err, "failed to unmarshal response body")
		}

		for _, apiResponse := range response {
			validSourceTypes = append(validSourceTypes, apiResponse.ApplicationType)
		}

		return nil
	},

	RunE: func(cmd *cobra.Command, args []string) error {
		var req *http.Request
		var err error
		client := &http.Client{}

		mctlCfg, err := config.GetMesheryCtl(viper.GetViper())
		if err != nil {
			return errors.Wrap(err, "error processing config")
		}

		deployURL := mctlCfg.GetBaseMesheryURL() + "/api/application/deploy"
		appURL := mctlCfg.GetBaseMesheryURL() + "/api/application"
		patternURL := mctlCfg.GetBaseMesheryURL() + "/api/pattern"

		// app name has been passed
		if len(args) > 0 {
			// Merge args to get app-name
			appName := strings.Join(args, "%20")

			// search and fetch apps with app-name
			utils.Log.Debug("Fetching apps")

			req, err = utils.NewRequest("GET", appURL+"?search="+appName, nil)
			if err != nil {
				return err
			}

			resp, err := client.Do(req)
			if err != nil {
				return err
			}

			var response *models.ApplicationsAPIResponse
			// failsafe (bad api call)
			if resp.StatusCode != 200 {
				return errors.Errorf("Response Status Code %d, possible Server Error", resp.StatusCode)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return errors.Wrap(err, utils.PerfError("failed to read response body"))
			}
			err = json.Unmarshal(body, &response)
			if err != nil {
				return errors.Wrap(err, "failed to unmarshal response body")
			}

			index := 0
			if len(response.Applications) == 0 {
				return errors.New("no apps found with the given name")
			} else if len(response.Applications) == 1 {
				appFile = response.Applications[0].ApplicationFile
			} else {
				// Multiple apps with same name
				index = multipleApplicationsConfirmation(response.Applications)
				appFile = response.Applications[index].ApplicationFile
			}
		} else {
			// Check if a valid source type is set
			if !isValidSource(sourceType) {
				return errors.Errorf("application source type (-s) invalid or not passed.\nAllowed source types: %s", strings.Join(validSourceTypes, ", "))
			}
			// Method to check if the entered file is a URL or not
			if validURL := govalidator.IsURL(file); !validURL {
				content, err := os.ReadFile(file)
				if err != nil {
					return err
				}
				text := string(content)

				// if --skip-save is not passed we save the apps first
				if !skipSave {
					jsonValues, err := json.Marshal(map[string]interface{}{
						"application_data": map[string]interface{}{
							"name":             path.Base(file),
							"application_file": text,
						},
						"save": true,
					})
					if err != nil {
						return err
					}
					req, err = utils.NewRequest("POST", appURL+"/"+sourceType, bytes.NewBuffer(jsonValues))
					if err != nil {
						return err
					}

					resp, err := client.Do(req)
					if err != nil {
						return err
					}
					utils.Log.Debug("saved app file")
					var response []*models.MesheryApplication
					// failsafe (bad api call)
					if resp.StatusCode != 200 {
						return errors.Errorf("Response Status Code %d, possible Server Error", resp.StatusCode)
					}
					defer resp.Body.Close()

					body, err := io.ReadAll(resp.Body)
					if err != nil {
						return errors.Wrap(err, utils.PerfError("failed to read response body"))
					}
					err = json.Unmarshal(body, &response)
					if err != nil {
						return errors.Wrap(err, "failed to unmarshal response body")
					}
				}

				// setup app file
				appFile = text
			} else {
				var jsonValues []byte
				url, path, err := utils.ParseURLGithub(file)
				if err != nil {
					return err
				}

				utils.Log.Debug(url)
				utils.Log.Debug(path)

				// save the app with Github URL
				if !skipSave {
					if path != "" {
						jsonValues, _ = json.Marshal(map[string]interface{}{
							"url":  url,
							"path": path,
							"save": true,
						})
					} else {
						jsonValues, _ = json.Marshal(map[string]interface{}{
							"url":  url,
							"save": true,
						})
					}
				} else { // we don't save the app
					if path != "" {
						jsonValues, _ = json.Marshal(map[string]interface{}{
							"url":  url,
							"path": path,
							"save": false,
						})
					} else {
						jsonValues, _ = json.Marshal(map[string]interface{}{
							"url":  url,
							"save": false,
						})
					}
				}
				req, err = utils.NewRequest("POST", appURL+"/"+sourceType, bytes.NewBuffer(jsonValues))
				if err != nil {
					return err
				}

				resp, err := client.Do(req)
				if err != nil {
					return err
				}
				utils.Log.Debug("remote hosted app request success")
				var response []*models.MesheryApplication
				// failsafe (bad api call)
				if resp.StatusCode != 200 {
					return errors.Errorf("Response Status Code %d, possible Server Error", resp.StatusCode)
				}
				defer resp.Body.Close()

				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return errors.Wrap(err, utils.PerfError("failed to read response body"))
				}
				err = json.Unmarshal(body, &response)
				if err != nil {
					return errors.Wrap(err, "failed to unmarshal response body")
				}

				// setup app file here
				appFile = response[0].ApplicationFile
			}
		}

		// Convert App File into Pattern File
		jsonValues, _ := json.Marshal(map[string]interface{}{
			"K8sManifest": appFile,
		})

		req, err = utils.NewRequest("POST", patternURL, bytes.NewBuffer(jsonValues))
		if err != nil {
			return err
		}

		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		var response []*models.MesheryPattern
		// bad api call
		if resp.StatusCode != 200 {
			return errors.Errorf("Response Status Code %d, possible Server Error", resp.StatusCode)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return errors.Wrap(err, utils.PerfError("failed to read response body"))
		}

		err = json.Unmarshal(body, &response)
		if err != nil {
			return errors.Wrap(err, "failed to unmarshal response body")
		}

		utils.Log.Debug("application file converted to pattern file")

		patternFile := response[0].PatternFile

		req, err = utils.NewRequest("POST", deployURL, bytes.NewBuffer([]byte(patternFile)))
		if err != nil {
			return err
		}

		res, err := client.Do(req)
		if err != nil {
			return err
		}

		defer res.Body.Close()
		body, err = io.ReadAll(res.Body)
		if err != nil {
			return err
		}

		if res.StatusCode == 200 {
			utils.Log.Info("app successfully onboarded")
		}
		utils.Log.Info(string(body))
		return nil
	},
}

func multipleApplicationsConfirmation(profiles []models.MesheryApplication) int {
	reader := bufio.NewReader(os.Stdin)

	for index, a := range profiles {
		fmt.Printf("Index: %v\n", index)
		fmt.Printf("Name: %v\n", a.Name)
		fmt.Printf("ID: %s\n", a.ID.String())
		fmt.Printf("ApplicationFile:\n")
		fmt.Printf(a.ApplicationFile)
		fmt.Println("---------------------")
	}

	for {
		fmt.Printf("Enter the index of app: ")
		response, err := reader.ReadString('\n')
		if err != nil {
			utils.Log.Info(err)
		}
		response = strings.ToLower(strings.TrimSpace(response))
		index, err := strconv.Atoi(response)
		if err != nil {
			utils.Log.Info(err)
		}
		if index < 0 || index >= len(profiles) {
			utils.Log.Info("Invalid index")
		} else {
			return index
		}
	}
}

func isValidSource(sType string) bool {
	for _, validType := range validSourceTypes {
		if validType == sType {
			return true
		}
	}

	return false
}

func init() {
	onboardCmd.Flags().StringVarP(&file, "file", "f", "", "Path to app file")
	onboardCmd.Flags().BoolVarP(&skipSave, "skip-save", "", false, "Skip saving a app")
	onboardCmd.Flags().StringVarP(&sourceType, "source-type", "s", "", "Type of source file (ex. manifest / compose / helm)")
}
