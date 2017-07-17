package service_yml

import (
	"github.com/cloud66/starter/common"
	"os"
	"io/ioutil"
	"gopkg.in/yaml.v2"
	"fmt"
	"strings"
)

func Transformer(filename string, formatTarget string) error {

	var err error
	_, err = os.Stat(formatTarget)

	// create file if not exists
	if os.IsNotExist(err) {
		var file, err = os.Create(formatTarget)
		CheckError(err)
		defer file.Close()
	} else {
		common.PrintError("File %s already exists. Will be overwritten.\n", formatTarget)
	}

	yamlFile, err := ioutil.ReadFile(filename)

	serviceYML := ServiceYml{
		Services: make(map[string]ServiceYMLService),
		Dbs:      []string{},
	}

	if err := yaml.Unmarshal([]byte(yamlFile), &serviceYML); err != nil {
		fmt.Println(err.Error())
	}

	file := copyToKubes(serviceYML)

	err = ioutil.WriteFile(formatTarget, file, 0644)
	if err != nil {
		return err
	}

	return nil
}

func copyToKubes(serviceYml ServiceYml) []byte {

	var file []byte
	var deploy KubesDeployment
	var deployments []KubesDeployment

	//Each service needs an unique nodePort, so we hand-pick to start
	//from 31111 and pray that it will not collide with other stuff.
	nodePort := 31111

	file = []byte("# Generated with <3 by Cloud66\n\n")

	for _, dbName := range serviceYml.Dbs {
		file = []byte(string(file) + "####### " + strings.ToUpper(string(dbName)) + " #######" + "\n")
		tags:= make(map[string]string, 1)
		tags["app"]=dbName

		service := KubesService{
			ApiVersion: "v1",
			Kind:       "Service",
			Metadata: Metadata{
				Name:   dbName + "-svc",
			},
			Spec: Spec{
				Type:  "ClusterIP",
				Ports: setDbServicePorts(dbName),
			},
		}
		deploy := KubesDeployment{ApiVersion: "extensions/v1beta1",
			Kind:                      "Deployment",
			Metadata: Metadata{
				Name: dbName + "-deployment",
			},
			Spec: Spec{
				Template: Template{
					Metadata: Metadata{
						Labels: tags,
					},
					PodSpec: PodSpec{
						Containers: []Containers{
							{
								Name:  dbName,
								Image: dbName + ":latest",
								Ports: setDbDeploymentPorts(dbName),
							},
						},
					},
				},
			},
		}
		//write db service
		fileServices, er := yaml.Marshal(service)
		CheckError(er)
		file = []byte(string(file) + string(finalFormat(fileServices)) + "---\n")

		//write db deployment
		deployments = append(deployments, deploy)

	}
	var deployPorts []KubesPorts
	var services []KubesService
	for serviceName, serviceSpecs := range serviceYml.Services {

		//if it has no image, output warning to user about the fact that each container needs one


		//gets ports to populate deployment and generates the required service(s)
		deployPorts, services, nodePort = generateServicesRequiredByPorts(serviceName, serviceSpecs, nodePort)

		//required by the kubes format
		if serviceSpecs.Tags==nil{
			serviceSpecs.Tags = make(map[string]string, 1)
		}
		serviceSpecs.Tags["app"] = serviceName

		deploy = KubesDeployment{ApiVersion: "extensions/v1beta1",
			Kind:                        "Deployment",
			Metadata: Metadata{
				Name: serviceName + "-deployment",
			},
			Spec: Spec{
				Template: Template{
					Metadata: Metadata{
						Labels: serviceSpecs.Tags,
					},
					PodSpec: PodSpec{
						TerminationGracePeriodSeconds: serviceSpecs.StopGrace,
						Containers: []Containers{
							{
								Name:       serviceName,
								Image:      serviceSpecs.Image,
								Command:    serviceSpecs.Command.Command,
								Ports:      deployPorts,
								WorkingDir: serviceSpecs.WorkDir,
								SecurityContext: SecurityContext{
									Priviliged: serviceSpecs.Privileged,
								},
								Lifecycle: Lifecycle{
									PostStart: Handler{
										Exec: Exec{
											Command: serviceSpecs.PostStartCommand.PostStartCommand,
										},
									},
									PreStop: Handler{
										Exec: Exec{
											Command: serviceSpecs.PreStopCommand.PreStopCommand,
										},
									},
								},
								Resources: KubesResources{
									Limits: Limits{
										Cpu:    serviceSpecs.Constraints.Resources.Cpu,
										Memory: serviceSpecs.Constraints.Resources.Memory,
									},
								},
							},
						},
					},
				},
			},
		}

		if serviceSpecs.Image==""{
			deploy.Spec.Template.PodSpec.Containers[0].Image = "#INSERT REQUIRED IMAGE"
			common.PrintlnWarning("The service \"%s\" has no image mentioned and each container needs one in Kubernetes format. Please add manually.", serviceName)
		}

		kubeVolumes := handleVolumes(serviceSpecs.Volumes)
		deploy.Spec.Template.PodSpec.Containers[0].VolumeMounts = kubeVolumes

		keys, values := getKeysValues(serviceSpecs.EnvVars)
		if len(keys) > 0 {
			for k := 0; k < len(keys); k++ {
				if values[k] == "\"\"" {
					values[k] = ""
				}
				env := EnvVar{
					Name:  keys[k],
					Value: values[k],
				}
				deploy.Spec.Template.PodSpec.Containers[0].Env = append(deploy.Spec.Template.PodSpec.Containers[0].Env, env)
			}
		}
		for _, service := range services {
			//file = []byte(string(file) + "####### " + strings.ToUpper(string(serviceName)) + " - Service #######\n" + "\n")
			fileServices, er := yaml.Marshal(service)
			CheckError(er)
			file = []byte(string(file)+ "####### " + strings.ToUpper(string(serviceName)) + " - Service #######\n" + "\n" + string(finalFormat(fileServices)) + "---\n")
		}
		deployments = append(deployments, deploy)
	}

	//delete last ror of ---
	if len(services)>0{
		file = file[:len(file)-4]
	}
	//write deployments last in order to make sure kubectl takes them into consideration
	for _, deploy := range deployments{
		fileDeployments, err := yaml.Marshal(deploy)
		CheckError(err)
		file = []byte(string(file) + "---\n####### " + strings.ToUpper(string(deploy.Metadata.Name)) + " #######\n" + string(finalFormat(fileDeployments)))
	}

	//delete last row of ---
	if len(deployments)>0 {
		file = file[:len(file)-4]
	}
	return file
}
