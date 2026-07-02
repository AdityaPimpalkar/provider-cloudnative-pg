#!/bin/bash

## ===== General environment variables for the CloudNativePG provider tests =====
export OPERATOR_ROOT_PATH=${OPERATOR_ROOT_PATH:-${PWD}}
echo "OPERATOR_ROOT_PATH=${OPERATOR_ROOT_PATH}"

## ======= Upstream DB operator params for testing ===============

export CNPG_OPERATOR_VERSION=${CNPG_OPERATOR_VERSION:-"0.28.3"}
echo "CNPG_OPERATOR_VERSION=${CNPG_OPERATOR_VERSION}"

export CNPG_PG_VERSION=${CNPG_PG_VERSION:-"17.10"}
echo "CNPG_PG_VERSION=${CNPG_PG_VERSION}"

## ============== K3D cluster configuration ===================

export K3D_CLUSTER_NAME=${K3D_CLUSTER_NAME:-"provider-cloudnative-pg-test"}
echo "K3D_CLUSTER_NAME=${K3D_CLUSTER_NAME}"
