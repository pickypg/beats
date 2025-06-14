import metricbeat
import os
import pytest
import sys
import unittest


ZK_FIELDS = metricbeat.COMMON_FIELDS + ["zookeeper"]

MNTR_FIELDS = ["latency.avg", "latency.max",
               "latency.min", "packets.received", "packets.sent",
               "outstanding_requests", "server_state", "znode_count",
               "watch_count", "ephemerals_count",
               "approximate_data_size", "num_alive_connections"]


@metricbeat.parameterized_with_supported_versions
class ZooKeeperMntrTest(metricbeat.BaseTest):

    COMPOSE_SERVICES = ['zookeeper']

    @unittest.skipUnless(metricbeat.INTEGRATION_TESTS, "integration test")
    @pytest.mark.tag('integration')
    def test_output(self):
        """
        ZooKeeper mntr module outputs an event.
        """
        self.render_config_template(modules=[{
            "name": "zookeeper",
            "metricsets": ["mntr"],
            "hosts": self.get_hosts(),
            "period": "5s"
        }])
        proc = self.start_beat()
        self.wait_until(lambda: self.output_lines() > 0)
        proc.check_kill_and_wait()
        self.assert_no_logged_warnings()

        output = self.read_output_json()
        self.assertTrue(len(output) >= 1, f"expected at least one output result, got: {output}")
        evt = output[0]

        self.assertCountEqual(self.de_dot(ZK_FIELDS), evt.keys())
        zk_mntr = evt["zookeeper"]["mntr"]

        zk_mntr.pop("pending_syncs", None)
        zk_mntr.pop("open_file_descriptor_count", None)
        zk_mntr.pop("synced_followers", None)
        zk_mntr.pop("max_file_descriptor_count", None)
        zk_mntr.pop("followers", None)

        self.assertCountEqual(self.de_dot(MNTR_FIELDS), zk_mntr.keys())

        self.assert_fields_are_documented(evt)

    @unittest.skipUnless(metricbeat.INTEGRATION_TESTS, "integration test")
    @pytest.mark.tag('integration')
    def test_output(self):
        """
        ZooKeeper server module outputs an event.
        """
        self.render_config_template(modules=[{
            "name": "zookeeper",
            "metricsets": ["server"],
            "hosts": self.get_hosts(),
            "period": "5s"
        }])
        proc = self.start_beat()
        self.wait_until(lambda: self.output_lines() > 0)
        proc.check_kill_and_wait()
        self.assert_no_logged_warnings()

        output = self.read_output_json()
        self.assertTrue(len(output) >= 1, f"expected at least one output result, got: {output}")
        evt = output[0]

        self.assertCountEqual(self.de_dot(ZK_FIELDS), evt.keys())
        zk_srvr = evt["zookeeper"]["server"]

        assert zk_srvr["connections"] >= 0

        self.assert_fields_are_documented(evt)

    @unittest.skipUnless(metricbeat.INTEGRATION_TESTS, "integration test")
    @pytest.mark.tag('integration')
    def test_connection(self):
        """
        ZooKeeper server module outputs an event.
        """
        self.render_config_template(modules=[{
            "name": "zookeeper",
            "metricsets": ["connection"],
            "hosts": self.get_hosts(),
            "period": "5s"
        }])
        proc = self.start_beat()
        self.wait_until(lambda: self.output_lines() > 0)
        proc.check_kill_and_wait()
        self.assert_no_logged_warnings()

        output = self.read_output_json()
        self.assertTrue(len(output) >= 1, f"expected at least one output result, got: {output}")
        evt = output[0]

        self.assertCountEqual(self.de_dot(ZK_FIELDS + ["client"]), evt.keys())
        zk_conns = evt["zookeeper"]["connection"]

        assert zk_conns["queued"] >= 0

        self.assert_fields_are_documented(evt)
