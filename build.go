// Go script for releasing or deploying grafana dashboards.
// This script expects to run within a gitlab ci pod.
package main

import (
	"archive/zip"
	"bufio"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httputil"
	"os"
	"os/exec"
	"strings"
)

// Helper method to return environment depending on the branch.
// To be used by the main deploy script to choose which grafana server to target
func SelectGrafanaServer(branch string) string {

	// If this is a project branch return ses, otherwise return dev
	if strings.Contains(branch, "project/") {
		return "tst"
	} else {
		return "dev"
	}
}

// Helper method to load a file into a string array of lines.
func FileToArray(file string) ([]string, error) {

	// Open the git diff file. This file is in .gitignore so it won't be commited.
	in_file, err := os.Open(file)
	if err != nil {
		panic("Failed to open: " + file)
		log.Fatal(err)
	}

	defer in_file.Close()

	// Read in the lines of file
	var lines []string
	scanner := bufio.NewScanner(in_file)

	// Add the next line to the array, trimming any new line character if it exists
	for scanner.Scan() {
		lines = append(lines, strings.TrimSuffix(scanner.Text(), "\n"))
	}

	return lines, scanner.Err()
}

// Helper function to compute the md5 of a string
func GetMD5Hash(text string) string {
	hasher := md5.New()
	hasher.Write([]byte(text))
	return hex.EncodeToString(hasher.Sum(nil))
}

// Render a dashboard into the dist folder
func Render(dashboard string, branch string) bool {

	dashboard_name_split := strings.Split(dashboard, "/")
	project_name := dashboard_name_split[1]
	dashboard_name := dashboard_name_split[len(dashboard_name_split)-1]

	// Generate a dashboard uid based on filename
	// Need to respect grafanas 40 char uid length limit
	// Include an element of chars unique to the branchname via md5
	ComputeMd5 := GetMD5Hash(strings.Replace(branch, "/", "", -1))[0:7]
	dashboard_uid := "uid-" + ComputeMd5 + strings.Replace(dashboard_name, ".json", "", -1)
	if len(dashboard_uid) >= 40 {
		dashboard_uid = dashboard_uid[0:39]
	}

	// If the dashboard file no longer exists for some reason then skip
	if _, err := os.Stat(dashboard); errors.Is(err, os.ErrNotExist) {
		fmt.Println("Dashboard file doesnt exist, skipping")
		return false
	}

	// Ensure a subfolder exists for the project
	os.Mkdir("dist/"+project_name, 0755)

	// Render dashboards built with jsonnet
	if strings.HasSuffix(dashboard_name, "jsonnet") {

		fmt.Println("Rendering jsonnet: " + dashboard_name)

		cmd := exec.Command("jsonnet", "-J", "vendor", dashboard, "--ext-str", "uid="+dashboard_uid)

		// Create the json file in the dist folder (dashboard is a string of the jsonnet file)
		outfile, err := os.Create("dist/" + project_name + "/" + dashboard_name[:len(dashboard_name)-3])
		if err != nil {
			log.Fatal(err)
		}

		fmt.Println(cmd.String())

		cmd.Stdout = outfile

		if err := cmd.Run(); err != nil {
			log.Fatal(err)
		}

	}

	// Render dashboards built with json
	if strings.HasSuffix(dashboard_name, "json") {

		fmt.Println("Rendering json: " + dashboard_name)

		// Check if the dashboard already has an id defined
		jsonfile, err := os.Open(dashboard)
		if err != nil {
			log.Fatal(err)
		}

		// Defer the closing of our jsonFile so that we can parse it later on
		defer jsonfile.Close()

		// Read our opened jsonfile as a byte array then parse the content.
		bytes, _ := ioutil.ReadAll(jsonfile)
		var parsed_dashboard map[string]interface{}
		json.Unmarshal([]byte(bytes), &parsed_dashboard)

		// Update dashboads uid to prevent clashes
		parsed_dashboard["uid"] = dashboard_uid

		fmt.Println(parsed_dashboard["uid"])

		// To create a new dashboard we need to ensure the id is set to null
		parsed_dashboard["id"] = nil

		// Write the file out to directory
		out_file, _ := json.MarshalIndent(parsed_dashboard, "", "   ")
		_ = ioutil.WriteFile("dist/"+project_name+"/"+dashboard_name, out_file, 0644)
	}

	fmt.Println("Rendered: " + dashboard_name)
	return true
}

// Find the changed files in a branch and renders them
// Returns true based on if a dashboard was rendered or not
func RenderChanged(branch string) bool {

	fmt.Println("Rendering changed dashboards")

	// Convert the git-diff file to an array
	changed, err := FileToArray("git-diff")
	if err != nil {
		log.Fatal(err)
	}

	// Print the array of changed files
	fmt.Println("Changed Files: ")
	fmt.Println(changed)

	files_to_deploy := false

	for _, file := range changed {

		// If the changed file is in the dashboards directory
		if strings.HasPrefix(file, "dashboards") {

			// Render the dashboard file
			Render(file, branch)

			files_to_deploy = true
		}
	}

	return files_to_deploy
}

// Helper method for printing httprequest debug data
func debug(data []byte, err error) {
	if err == nil {
		fmt.Printf("%s\n\n", data)
	} else {
		log.Fatalf("%s\n\n", err)
	}
}

// Helper method to do all the api requests to grafana
func DoPOST(url string, payload string) {

	// Retrieve authentication details from pipeline
	GRAFANA_USER, ok := os.LookupEnv("GRAFANA_USER")
	if !ok {
		panic("GRAFANA_USER env not set")
	}
	GRAFANA_PASSWORD, ok := os.LookupEnv("GRAFANA_PASSWORD")
	if !ok {
		panic("GRAFANA_PASSWORD env not set")
	}

	body := strings.NewReader(payload)

	var response_body []byte
	var response *http.Response
	var request *http.Request

	request, err := http.NewRequest("POST", url, body)

	if err == nil {

		request.Header.Add("Content-Type", "application/json")
		request.SetBasicAuth(os.ExpandEnv(GRAFANA_USER), os.ExpandEnv(GRAFANA_PASSWORD))

		// Uncomment this to debug requests
		//debug(httputil.DumpRequestOut(request, true))

		response, err = (&http.Client{}).Do(request)
	}

	if err == nil {

		defer response.Body.Close()

		// Uncomment this to debug responses
		debug(httputil.DumpResponse(response, true))

		response_body, err = ioutil.ReadAll(response.Body)
	}

	if err == nil {
		fmt.Printf("%s", response_body)
	} else {
		log.Fatalf("ERROR: %s", err)
	}
}

// Post to create a grafana folder for the dashboards
func CreateGrafanaFolder(folder_uid string, folder_name string, grafana_server string) {

	fmt.Println("Creating grafana folder: " + folder_name + ", uid: " + folder_uid)

	payload := `{"uid": "` + folder_uid + `", "title": "` + folder_name + `", "overwrite": true}`
	//fmt.Println(payload) // Uncomment to debug payload

	if grafana_server == "tst" {
		// test
    DoPOST("${GRAFANA_SERVER_TEST}/api/folders", payload)
	} else {
		// dev
		DoPOST("${GRAFANA_SERVER_DEV}/api/folders", payload)
	}
}

// Deploy an individual dashboard to a given folder on given grafana server
func DeployDashboard(dashboard string, folder_uid string, grafana_server string) {

	fmt.Println("Deploying: " + dashboard)

	dashboard_command, err := exec.Command("jq", "-c", ".", dashboard).Output()
	if err != nil {
		log.Fatal(err)
	}

	dashboard_string := strings.TrimSuffix(string(dashboard_command), "\n")

	payload := `{"dashboard": ` + dashboard_string + `, "folderUid": "` + folder_uid + `", "overwrite": true}`
	//fmt.Println(payload) // Uncomment to debug payloads

	if grafana_server == "ses" {
		// test
		DoPOST("${GRAFANA_SERVER_TEST}/api/dashboards/db", payload)

	} else {
		// dev
		DoPOST("${GRAFANA_SERVER_DEV}/api/dashboards/db", payload)
	}
}

// Helper recursive method to go through generated dashboards and deploy each one
func DeployAllDashboards(path string, folder_uid string, grafana_server string) {

	fmt.Println("Deploying Dashboards")

	// Loop over each file in path
	items, _ := ioutil.ReadDir(path)
	for _, item := range items {

		if item.IsDir() && !strings.Contains(item.Name(), "rlt") {

			// If the item is a directory and does not relate to realtime drill down to that level
			DeployAllDashboards(path+"/"+item.Name(), folder_uid, grafana_server)

		} else {

			// Otherwise if it's an ordinary dashboard file deploy it
			DeployDashboard(path+"/"+item.Name(), folder_uid, grafana_server)
		}
	}
}

func main() {

	fmt.Println("Pipeline build script started")

	// Command Line Flags
	// These are pointers, not the actual values. Access by using *varname.
	projectPointer := flag.String("project", "", "Set project name for long lived branches.")
	deployPointer := flag.Bool("deploy", false, "Turn on flag to deploy rendered dashboards to grafana.")
  
	// Parse Command Line flags
	flag.Parse()

	// Retrieve branch name from environment
	branch, ok := os.LookupEnv("CI_COMMIT_BRANCH")
	if !ok {
		panic("CI_COMMIT_BRANCH env not set")
	}

	// Create folder to render Dashboards to. This folder is in .gitignore so it won't be commited.
	fmt.Println("Creating dist Folder")
	os.Mkdir("dist/", 0755)

	// If we are doing a deployment
	if *deployPointer {

		fmt.Println("Running grafana deploy")

		if *projectPointer == "" {
			panic("Project has not been specified. This should be set by pipeline.")
		}

		// Clean the branch name to remove slashes
		clean_branch := strings.Replace(branch, "/", "", -1)
		fmt.Println("Project: " + clean_branch)

		// Identify any files that have changed
		files_to_deploy := RenderChanged(clean_branch)

		// If renderchanged returned true, then there are dashboards to deploy
		if files_to_deploy {

			// We base our grafana folder uid on the branch name limited to 40 chars.
			// Grafana has a limit of 40 characters for folder uids
			folder_uid := clean_branch
			if len(clean_branch) >= 40 {
				folder_uid = clean_branch[0:39]
			}

			// Identify the grafana server based on branch
			grafana_server := SelectGrafanaServer(branch)

			// Create a folder on that server for the dashboards
			CreateGrafanaFolder(folder_uid, clean_branch, grafana_server)

			// Deploy the dashboards to that folder
			DeployAllDashboards("dist", folder_uid, grafana_server)

			// Report success
			fmt.Println(" ")
			fmt.Println(" ")
			fmt.Println("Dashboards deployed to " + grafana_server + "/grafana/dashboards/")
		}
	}
}
