package ecs

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
)

type ecsClientInterface interface {
	DescribeTaskStatus(ctx context.Context, taskARN string) (string, error)
	ListClusters(ctx context.Context) ([]string, error)
	RunTask(ctx context.Context) (string, error)
}

type awsEcsClient struct {
	cluster   string
	ecsClient *ecs.Client
}

func (c awsEcsClient) DescribeTaskStatus(ctx context.Context, taskARN string) (string, error) {
	input := ecs.DescribeTasksInput{
		Cluster: aws.String("jrasell-test"),
		Tasks:   []string{taskARN},
	}

	resp, err := c.ecsClient.DescribeTasksRequest(&input).Send(ctx)
	if err != nil {
		return "", err
	}
	return *resp.Tasks[0].LastStatus, nil
}

func (c awsEcsClient) ListClusters(ctx context.Context) ([]string, error) {
	if output, err := c.ecsClient.ListClustersRequest(nil).Send(ctx); err != nil {
		return nil, err
	} else {
		return output.ClusterArns, nil
	}
}

func (c awsEcsClient) RunTask(ctx context.Context) (string, error) {
	input := ecs.RunTaskInput{
		Cluster:    aws.String("jrasell-test"),
		Count:      aws.Int64(1),
		LaunchType: "FARGATE",
		NetworkConfiguration: &ecs.NetworkConfiguration{
			AwsvpcConfiguration: &ecs.AwsVpcConfiguration{
				AssignPublicIp: "ENABLED",
				SecurityGroups: []string{"sg-05f444f6c0dda876d"},
				Subnets:        []string{"subnet-0cd4b2ec21331a144", "subnet-0da9019dcab8ae2f1"},
			},
		},
		StartedBy:      aws.String("nomad-remote-ecs-driver"),
		TaskDefinition: aws.String("jrasll-test:1"),
	}

	resp, err := c.ecsClient.RunTaskRequest(&input).Send(ctx)
	if err != nil {
		return "", err
	}
	return *resp.RunTaskOutput.Tasks[0].TaskArn, nil
}
