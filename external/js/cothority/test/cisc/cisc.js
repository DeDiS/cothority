//"use strict";
const chai = require("chai");
const cothority = require("../../lib");
const kyber = require("@dedis/kyber-js");
const helpers = require("../helpers.js");
const child_process = require("child_process");
const fs = require("fs");
const co = require("co");

const curve = new kyber.curve.edwards25519.Curve();
const proto = cothority.protobuf;
const cisc = cothority.cisc;
const misc = cothority.misc;
const net = cothority.net;
const expect = chai.expect;
const build_dir = process.cwd() + "/test/cisc/build";
describe("cisc client", () => {
  var proc;
  after(function() {
    helpers.killGolang(proc);
  });

  it("can retrieve updates from conodes", done => {
    var fn = co.wrap(function*() {
      [roster, id] = helpers.readSkipchainInfo(build_dir);
      const client = new cisc.Client(curve, roster, id);
      cisc_data = yield client.getLatestCISCData();

      // try to read it from a roster socket
      //  and compare if we have the same results
      const socket = new net.RosterSocket(roster, "Identity");
      const requestStr = "DataUpdate";
      const responseStr = "DataUpdateReply";
      const request = { id: misc.hexToUint8Array(id) };
      cisc_data2 = yield socket.send(requestStr, responseStr, request);

      expect(cisc_data).to.be.deep.equal(cisc_data2.data);

      kvStore = cisc_data.storage;
      kvStore2 = yield client.getStorage();

      expect(kvStore).to.be.deep.equal(kvStore2);

      done();
    });
    helpers
      .runGolang(build_dir, data => data.match(/OK/))
      .then(proces => {
        proc = proces;
        return Promise.resolve(true);
      })
      .then(fn)
      .catch(err => {
        done(err);
        throw err;
      });
  }).timeout(5000);
});
