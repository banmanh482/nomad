package ecs

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
)

type ecsClientInterface interface {
	DescribeCluster(ctx context.Context) error
	DescribeTaskStatus(ctx context.Context, taskARN string) (string, error)
	RunTask(ctx context.Context, cfg TaskConfig) (string, error)
	StopTask(ctx context.Context, task, reason string) error
}

type awsEcsClient struct {
	cluster   string
	ecsClient *ecs.Client
}

func (c awsEcsClient) DescribeCluster(ctx context.Context) error {
	input := ecs.DescribeClustersInput{Clusters: []string{c.cluster}}

	resp, err := c.ecsClient.DescribeClustersRequest(&input).Send(ctx)
	if err != nil {
		return err
	}

	if len(resp.Clusters) > 1 || len(resp.Clusters) < 1 {
		return fmt.Errorf("AWS returned %v ECS clusters, expected 1", len(resp.Clusters))
	}

	if *resp.Clusters[0].Status != "ACTIVE" {
		return fmt.Errorf("ECS cluster status: %s", *resp.Clusters[0].Status)
	}

	return nil
}

func (c awsEcsClient) DescribeTaskStatus(ctx context.Context, taskARN string) (string, error) {
	input := ecs.DescribeTasksInput{
		Cluster: aws.String(c.cluster),
		Tasks:   []string{taskARN},
	}

	resp, err := c.ecsClient.DescribeTasksRequest(&input).Send(ctx)
	if err != nil {
		return "", err
	}
	return *resp.Tasks[0].LastStatus, nil
}

func (c awsEcsClient) RunTask(ctx context.Context, cfg TaskConfig) (string, error) {
	input := c.buildTaskInput(cfg)

	if err := input.Validate(); err != nil {
		return "", nil
	}

	resp, err := c.ecsClient.RunTaskRequest(input).Send(ctx)
	if err != nil {
		return "", err
	}
	return *resp.RunTaskOutput.Tasks[0].TaskArn, nil
}

func (c awsEcsClient) buildTaskInput(cfg TaskConfig) *ecs.RunTaskInput {
	input := ecs.RunTaskInput{
		Cluster:              aws.String(c.cluster),
		Count:                aws.Int64(1),
		StartedBy:            aws.String("nomad-ecs-driver"),
		NetworkConfiguration: &ecs.NetworkConfiguration{AwsvpcConfiguration: &ecs.AwsVpcConfiguration{}},
	}

	if cfg.Task.LaunchType != "" {
		if cfg.Task.LaunchType == "EC2" {
			input.LaunchType = ecs.LaunchTypeEc2
		} else if cfg.Task.LaunchType == "FARGATE" {
			input.LaunchType = ecs.LaunchTypeFargate
		}
	}

	if cfg.Task.TaskDefinition != "" {
		input.TaskDefinition = aws.String(cfg.Task.TaskDefinition)
	}

	// Handle the task networking setup.
	if cfg.Task.NetworkConfiguration.TaskAWSVPCConfiguration.AssignPublicIP != "" {
		input.NetworkConfiguration.AwsvpcConfiguration.AssignPublicIp = "ENABLED"
	}
	if len(cfg.Task.NetworkConfiguration.TaskAWSVPCConfiguration.SecurityGroups) > 0 {
		input.NetworkConfiguration.AwsvpcConfiguration.SecurityGroups = cfg.Task.NetworkConfiguration.TaskAWSVPCConfiguration.SecurityGroups
	}
	if len(cfg.Task.NetworkConfiguration.TaskAWSVPCConfiguration.Subnets) > 0 {
		input.NetworkConfiguration.AwsvpcConfiguration.Subnets = cfg.Task.NetworkConfiguration.TaskAWSVPCConfiguration.Subnets
	}

	return &input
}

func (c awsEcsClient) StopTask(ctx context.Context, task, reason string) error {
	input := ecs.StopTaskInput{
		Cluster: aws.String(c.cluster),
		Task:    &task,
	}

	if reason == "" {
		reason = "stopped by nomad-ecs-driver automation"
	}
	input.Reason = aws.String(reason)

	_, err := c.ecsClient.StopTaskRequest(&input).Send(ctx)
	return err
}
