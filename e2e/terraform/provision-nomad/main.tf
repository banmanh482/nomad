locals {
  provision_script = var.platform == "windows_amd64" ? "powershell C:/opt/provision.ps1" : "/opt/provision.sh"

  custom_path = dirname("${path.root}/config/custom/")

  custom_config_files = compact(setunion(
    fileset(local.custom_path, "nomad/*.hcl"),
    fileset(local.custom_path, "nomad/${var.role}/*.hcl"),
    fileset(local.custom_path, "nomad/${var.role}/indexed/*${var.index}.hcl"),
    fileset(local.custom_path, "consul/*.json"),
    fileset(local.custom_path, "consul/${var.role}/*.json"),
    fileset(local.custom_path, "consul${var.role}indexed/*${var.index}*.json"),
    fileset(local.custom_path, "vault/*.hcl"),
    fileset(local.custom_path, "vault${var.role}*.hcl"),
    fileset(local.custom_path, "vault${var.role}indexed/*${var.index}.hcl"),
  ))

  # abstract-away platform-specific parameter expectations
  _arg = var.platform == "windows_amd64" ? "-" : "--"
}

resource "null_resource" "provision_nomad" {

  depends_on = [
    null_resource.upload_custom_configs,
    null_resource.upload_nomad_binary
  ]

  # no need to re-run if nothing changes
  triggers = {
    script = data.template_file.provision_script.rendered
  }


  connection {
    type            = "ssh"
    user            = var.connection.user
    host            = var.connection.host
    port            = var.connection.port
    private_key     = file(var.connection.private_key)
    target_platform = var.platform == "windows_amd64" ? "windows" : "unix"
    timeout         = "15m"
  }

  provisioner "remote-exec" {
    inline = [data.template_file.provision_script.rendered]
  }

}

data "template_file" "provision_script" {
  template = "${local.provision_script}${data.template_file.arg_nomad_sha.rendered}${data.template_file.arg_nomad_version.rendered}${data.template_file.arg_nomad_binary.rendered}${data.template_file.arg_nomad_enterprise.rendered}${data.template_file.arg_nomad_acls.rendered}${data.template_file.arg_profile.rendered}${data.template_file.arg_role.rendered}${data.template_file.arg_index.rendered}"
}

data "template_file" "arg_nomad_sha" {
  template = var.nomad_sha != "" && var.nomad_local_binary == "" ? " ${local._arg}nomad_sha ${var.nomad_sha}" : ""
}

data "template_file" "arg_nomad_version" {
  template = var.nomad_version != "" && var.nomad_sha == "" && var.nomad_local_binary == "" ? " ${local._arg}nomad_version ${var.nomad_version}" : ""
}

data "template_file" "arg_nomad_binary" {
  template = var.nomad_local_binary != "" ? " ${local._arg}nomad_binary /tmp/nomad" : ""
}

data "template_file" "arg_nomad_enterprise" {
  template = var.nomad_enterprise ? " ${local._arg}enterprise" : ""
}

data "template_file" "arg_nomad_acls" {
  template = var.nomad_acls ? " ${local._arg}nomad_acls" : ""
}

data "template_file" "arg_profile" {
  template = var.profile != "" ? " ${local._arg}config_profile ${var.profile}" : ""
}

data "template_file" "arg_role" {
  template = var.role != "" ? " ${local._arg}role ${var.role}" : ""
}

data "template_file" "arg_index" {
  template = var.index != "" ? " ${local._arg}index ${var.index}" : ""
}

resource "null_resource" "upload_nomad_binary" {

  count      = var.nomad_local_binary != "" ? 1 : 0
  depends_on = [null_resource.upload_custom_configs]
  triggers = {
    nomad_binary_sha = filemd5(var.nomad_local_binary)
  }

  connection {
    type            = "ssh"
    user            = var.connection.user
    host            = var.connection.host
    port            = var.connection.port
    private_key     = file(var.connection.private_key)
    target_platform = var.platform == "windows_amd64" ? "windows" : "unix"
    timeout         = "15m"
  }

  provisioner "file" {
    source      = var.nomad_local_binary
    destination = "/tmp/nomad"
  }
}

resource "null_resource" "upload_custom_configs" {

  count = var.profile == "custom" ? 1 : 0
  triggers = {
    hashes = join(",", [for file in local.custom_config_files : filemd5("${local.custom_path}/${file}")])
  }

  connection {
    type            = "ssh"
    user            = var.connection.user
    host            = var.connection.host
    port            = var.connection.port
    private_key     = file(var.connection.private_key)
    target_platform = var.platform == "windows_amd64" ? "windows" : "unix"
    timeout         = "15m"
  }

  provisioner "file" {
    source      = local.custom_path
    destination = "/tmp/"
  }
}
